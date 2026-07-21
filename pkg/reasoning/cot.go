package reasoning

import "context"

// CoTReasoner implements chain-of-thought by appending a "think step by
// step" instruction to the system prompt and stripping the thinking trace
// from the final answer.
type CoTReasoner struct{}

var _ Engine = (*CoTReasoner)(nil)

const cotInstruction = `You are a reasoning assistant. Think step by step, then provide your final answer after "Thinking Process".`

func (c *CoTReasoner) Paradigm() Paradigm { return ParadigmCoT }

func (c *CoTReasoner) Reason(ctx context.Context, req *Request) (*Result, error) {
	augmented := req.SystemPrompt
	if augmented != "" {
		augmented += "\n\n"
	}
	augmented += cotInstruction

	content, err := req.ModelFunc(ctx, augmented, req.Messages)
	if err != nil {
		return nil, err
	}

	return &Result{
		Content:  content,
		Paradigm: ParadigmCoT,
	}, nil
}
