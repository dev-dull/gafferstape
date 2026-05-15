package client

import "context"

type propertyListData struct {
	Properties []Property `json:"properties"`
}

// GetProperties returns every property on the logged-in account.
func (c *Client) GetProperties(ctx context.Context) ([]Property, error) {
	data, err := fetch[propertyListData](ctx, c, "/api/property/get-all", nil)
	if err != nil {
		return nil, err
	}
	return data.Properties, nil
}
