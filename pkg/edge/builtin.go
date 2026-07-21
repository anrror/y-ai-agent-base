package edge

import "context"

// CloudOnly routes every request to the cloud endpoint. This is the
// simplest mode with no edge infrastructure.
type CloudOnly struct {
	cfg Config
}

var _ Manager = (*CloudOnly)(nil)

func NewCloudOnly(cfg Config) *CloudOnly { return &CloudOnly{cfg: cfg} }

func (c *CloudOnly) Mode() Mode                 { return ModeCloudOnly }
func (c *CloudOnly) Config() Config             { return c.cfg }

func (c *CloudOnly) Decide(_ context.Context, _ *Request) (*Decision, error) {
	return &Decision{
		Mode:         ModeCloudOnly,
		Endpoint:     c.cfg.CloudEndpoint,
		UseEdgeCache: false,
		SyncRequired: false,
	}, nil
}

// EdgeAssist routes to cloud primarily, but uses the edge node for
// caching and pre-processing to reduce latency.
type EdgeAssist struct {
	cfg Config
}

var _ Manager = (*EdgeAssist)(nil)

func NewEdgeAssist(cfg Config) *EdgeAssist { return &EdgeAssist{cfg: cfg} }

func (e *EdgeAssist) Mode() Mode                 { return ModeEdgeAssist }
func (e *EdgeAssist) Config() Config             { return e.cfg }

func (e *EdgeAssist) Decide(_ context.Context, req *Request) (*Decision, error) {
	return &Decision{
		Mode:         ModeEdgeAssist,
		Endpoint:     e.cfg.CloudEndpoint,
		UseEdgeCache: req.LatencySensitive,
		SyncRequired: false,
	}, nil
}

// EdgeOnly handles all requests on the edge node without cloud dependency.
// Useful for offline-capable deployments.
type EdgeOnly struct {
	cfg Config
}

var _ Manager = (*EdgeOnly)(nil)

func NewEdgeOnly(cfg Config) *EdgeOnly { return &EdgeOnly{cfg: cfg} }

func (e *EdgeOnly) Mode() Mode                 { return ModeEdgeOnly }
func (e *EdgeOnly) Config() Config             { return e.cfg }

func (e *EdgeOnly) Decide(_ context.Context, _ *Request) (*Decision, error) {
	return &Decision{
		Mode:         ModeEdgeOnly,
		Endpoint:     e.cfg.EdgeEndpoint,
		UseEdgeCache: true,
		SyncRequired: true,
	}, nil
}

// Hybrid dynamically selects edge or cloud based on latency sensitivity
// and estimated token count. Small, latency-sensitive requests go to edge;
// large or complex requests go to cloud.
type Hybrid struct {
	cfg Config
}

var _ Manager = (*Hybrid)(nil)

func NewHybrid(cfg Config) *Hybrid { return &Hybrid{cfg: cfg} }

func (h *Hybrid) Mode() Mode                 { return ModeHybrid }
func (h *Hybrid) Config() Config             { return h.cfg }

func (h *Hybrid) Decide(_ context.Context, req *Request) (*Decision, error) {
	if req.LatencySensitive && req.EstimatedTokens < 2000 {
		return &Decision{
			Mode:         ModeEdgeOnly,
			Endpoint:     h.cfg.EdgeEndpoint,
			UseEdgeCache: true,
			SyncRequired: true,
		}, nil
	}
	return &Decision{
		Mode:         ModeCloudOnly,
		Endpoint:     h.cfg.CloudEndpoint,
		UseEdgeCache: false,
		SyncRequired: false,
	}, nil
}
