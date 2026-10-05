package servers

import (
	"context"
	"net/http"
)

// httpNewRequest is factored out for clarity.
func httpNewRequest(ctx context.Context, url string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "quarry-daemon")
	return req, nil
}
