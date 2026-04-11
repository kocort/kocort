package download

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ProgressCallback receives download progress updates (bytes downloaded, total bytes).
// total may be -1 if the server did not provide Content-Length.
type ProgressCallback func(downloaded, total int64)

// DownloadAndExtract downloads an archive from url and extracts it to targetDir.
// The archive format is inferred from the URL extension (.zip or .tar.gz).
// The optional progress callback receives byte-level progress updates.
func DownloadAndExtract(ctx context.Context, archiveURL, targetDir string, client *http.Client, progress ProgressCallback) error {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, archiveURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d from %s", resp.StatusCode, archiveURL)
	}

	var body io.Reader = resp.Body
	if progress != nil {
		body = &progressReader{r: resp.Body, total: resp.ContentLength, onProgress: progress}
	}

	if strings.HasSuffix(archiveURL, ".zip") {
		return ExtractZip(body, targetDir)
	}
	return ExtractTarGz(body, targetDir)
}

// ExtractTarGz extracts a .tar.gz archive from r into targetDir.
func ExtractTarGz(r io.Reader, targetDir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read: %w", err)
		}

		target := filepath.Join(targetDir, header.Name)

		// Security: prevent path traversal
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(targetDir)) {
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}
	return nil
}

// ExtractZip extracts a .zip archive from r into targetDir.
// Since zip requires seeking, the stream is first saved to a temp file.
func ExtractZip(r io.Reader, targetDir string) error {
	tmpFile, err := os.CreateTemp("", "kocort-llama-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	if _, err := io.Copy(tmpFile, r); err != nil {
		return err
	}

	stat, err := tmpFile.Stat()
	if err != nil {
		return err
	}

	zr, err := zip.NewReader(tmpFile, stat.Size())
	if err != nil {
		return err
	}

	for _, f := range zr.File {
		target := filepath.Join(targetDir, f.Name)

		// Security: prevent path traversal
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(targetDir)) {
			continue
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(target, 0o755)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// progressReader wraps an io.Reader to report download progress.
type progressReader struct {
	r          io.Reader
	downloaded int64
	total      int64
	onProgress ProgressCallback
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	if n > 0 {
		pr.downloaded += int64(n)
		pr.onProgress(pr.downloaded, pr.total)
	}
	return n, err
}
