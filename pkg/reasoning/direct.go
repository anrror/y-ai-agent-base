package reasoning

import "context"

// DirectReasoner implements the simplest paradigm: it passes the request
// straight through to the LLM with no additional scaffolding.
type DirectReasoner struct{}

var _ Engine = (*DirectReasoner)(nil)

func (d *DirectReasoner) Paradigm() Paradigm { return ParadigmDirect }

func (d *DirectReasoner) Reason(ctx context.Context, req *Request) (*Result, error) {
	content, err := req.ModelFunc(ctx, req.SystemPrompt, req.Messages)
	if err != nil {
		return nil, err
	}
	return &Result{
		Content:  content,
		Paradigm: ParadigmDirect,
	}, nil
}
