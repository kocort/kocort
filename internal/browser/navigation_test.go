package browser

import "testing"

func TestValidateNavigationURL(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{name: "https", raw: "https://example.com", wantErr: false},
		{name: "http", raw: "http://example.com", wantErr: false},
		{name: "about", raw: "about:blank", wantErr: false},
		{name: "missing scheme", raw: "example.com", wantErr: true},
		{name: "javascript", raw: "javascript:alert(1)", wantErr: true},
		{name: "data", raw: "data:text/html,hello", wantErr: true},
		{name: "file", raw: "file:///tmp/demo.html", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateNavigationURL(tc.raw)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for %q", tc.raw)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.raw, err)
			}
		})
	}
}
