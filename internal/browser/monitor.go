package browser

import (
	"fmt"
	"strings"
	"sync"
	"time"

	playwright "github.com/playwright-community/playwright-go"
)

type observedPageState struct {
	mu            sync.Mutex
	Console       []map[string]any
	Errors        []map[string]any
	Requests      []map[string]any
	NextReqID     int
	RequestIDs    sync.Map
	DownloadArmID int
	DownloadWait  chan playwright.Download
}

var observedPages sync.Map
var observedContexts sync.Map

type observedContextState struct {
	mu          sync.Mutex
	TraceActive bool
}

func ensureObservedPageState(page playwright.Page) *observedPageState {
	if existing, ok := observedPages.Load(page); ok {
		return existing.(*observedPageState)
	}
	state := &observedPageState{
		Console:  []map[string]any{},
		Errors:   []map[string]any{},
		Requests: []map[string]any{},
	}
	actual, loaded := observedPages.LoadOrStore(page, state)
	if loaded {
		return actual.(*observedPageState)
	}
	ensureObservedContextState(page.Context())
	page.OnConsole(func(msg playwright.ConsoleMessage) {
		state.mu.Lock()
		defer state.mu.Unlock()
		entry := map[string]any{
			"type":      msg.Type(),
			"text":      msg.Text(),
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}
		if location := msg.Location(); location != nil {
			entry["location"] = map[string]any{
				"url":          location.URL,
				"lineNumber":   location.LineNumber,
				"columnNumber": location.ColumnNumber,
			}
		}
		state.Console = appendBounded(state.Console, entry, 500)
	})
	page.OnPageError(func(err error) {
		state.mu.Lock()
		defer state.mu.Unlock()
		entry := map[string]any{
			"message":   err.Error(),
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}
		type namedError interface{ Name() string }
		if v, ok := any(err).(namedError); ok {
			entry["name"] = v.Name()
		}
		if stack := strings.TrimSpace(fmt.Sprintf("%+v", err)); stack != "" && stack != err.Error() {
			entry["stack"] = stack
		}
		state.Errors = appendBounded(state.Errors, entry, 200)
	})
	page.OnRequest(func(req playwright.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()
		state.NextReqID++
		id := fmt.Sprintf("r%d", state.NextReqID)
		record := map[string]any{
			"id":           id,
			"timestamp":    time.Now().UTC().Format(time.RFC3339),
			"method":       req.Method(),
			"url":          req.URL(),
			"resourceType": req.ResourceType(),
		}
		state.Requests = appendBounded(state.Requests, record, 500)
		state.RequestIDs.Store(req, id)
	})
	page.OnResponse(func(resp playwright.Response) {
		state.mu.Lock()
		defer state.mu.Unlock()
		id, ok := state.requestID(resp.Request())
		if !ok {
			return
		}
		if rec := findRequestRecord(state.Requests, id); rec != nil {
			rec["status"] = resp.Status()
			rec["ok"] = resp.Ok()
		}
	})
	page.OnRequestFailed(func(req playwright.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()
		id, ok := state.requestID(req)
		if !ok {
			return
		}
		rec := findRequestRecord(state.Requests, id)
		if rec == nil {
			return
		}
		if failure := req.Failure(); failure != nil {
			rec["failureText"] = failure.Error()
		}
		rec["ok"] = false
	})
	page.OnDownload(func(download playwright.Download) {
		state.mu.Lock()
		waiter := state.DownloadWait
		armID := state.DownloadArmID
		state.mu.Unlock()
		if waiter == nil || armID == 0 {
			return
		}
		select {
		case waiter <- download:
		default:
		}
	})
	return state
}

func ensureObservedContextState(ctx playwright.BrowserContext) *observedContextState {
	if existing, ok := observedContexts.Load(ctx); ok {
		return existing.(*observedContextState)
	}
	state := &observedContextState{}
	actual, loaded := observedContexts.LoadOrStore(ctx, state)
	if loaded {
		return actual.(*observedContextState)
	}
	for _, page := range ctx.Pages() {
		ensureObservedPageState(page)
	}
	ctx.OnPage(func(page playwright.Page) {
		ensureObservedPageState(page)
	})
	return state
}

func (s *observedPageState) requestID(req playwright.Request) (string, bool) {
	if req == nil {
		return "", false
	}
	value, ok := s.RequestIDs.Load(req)
	if !ok {
		return "", false
	}
	id, _ := value.(string)
	id = strings.TrimSpace(id)
	return id, id != ""
}

func findRequestRecord(items []map[string]any, id string) map[string]any {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	for i := len(items) - 1; i >= 0; i-- {
		value, _ := items[i]["id"].(string)
		if strings.TrimSpace(value) == id {
			return items[i]
		}
	}
	return nil
}

func appendBounded(slice []map[string]any, item map[string]any, max int) []map[string]any {
	slice = append(slice, item)
	if len(slice) <= max {
		return slice
	}
	return append([]map[string]any{}, slice[len(slice)-max:]...)
}

func cloneRecords(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		next := map[string]any{}
		for key, value := range item {
			next[key] = value
		}
		out = append(out, next)
	}
	return out
}

func filterRequestRecords(items []map[string]any, filter string) []map[string]any {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return cloneRecords(items)
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		urlValue, _ := item["url"].(string)
		if strings.Contains(strings.ToLower(urlValue), filter) {
			next := map[string]any{}
			for key, value := range item {
				next[key] = value
			}
			out = append(out, next)
		}
	}
	return out
}

func consolePriority(level string) int {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "error":
		return 3
	case "warning":
		return 2
	case "info", "log":
		return 1
	case "debug":
		return 0
	default:
		return 1
	}
}
