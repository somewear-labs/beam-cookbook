package stream

import "context"

const commandOutputService = "grid-command-output.v1"

func RegisterCommandOutput(endpoint *Endpoint, handler Handler) error {
	return endpoint.Register(commandOutputService, handler)
}

func OpenCommandOutput(ctx context.Context, endpoint *Endpoint, peerAccountID int64, route Route) (*Stream, error) {
	return endpoint.Open(ctx, peerAccountID, route, commandOutputService)
}
