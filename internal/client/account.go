package client

import "context"

// GetAccountInfo returns the homeowner profile and full property +
// inverter metadata.
func (c *Client) GetAccountInfo(ctx context.Context) (Account, error) {
	return fetch[Account](ctx, c, "/api/auth/get-account-info", nil)
}
