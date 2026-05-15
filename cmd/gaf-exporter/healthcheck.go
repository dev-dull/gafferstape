package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// healthcheck performs a GET against /healthz on the given listen
// address and returns nil on 200, an error otherwise. Designed for the
// Docker HEALTHCHECK directive: since distroless images ship without a
// shell or curl, the binary itself is the only thing available to run.
func healthcheck(listenAddr string) error {
	addr := listenAddr
	if addr == "" {
		addr = ":9876"
	}
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	url := "http://" + addr + "/healthz"

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("healthcheck %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck %s: status %d", url, resp.StatusCode)
	}
	return nil
}
