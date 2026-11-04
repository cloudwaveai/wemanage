package wmjournal

import (
	"context"

	"aiseat/internal/workdigest"
)

// ConfiguredExtractor reuses the existing configured model identity while
// executing the analysis process independently on the database host.
type ConfiguredExtractor struct{ Runner *workdigest.Runner }

func (c ConfiguredExtractor) Extract(ctx context.Context, input string) (string, string, Usage, error) {
	text, model, usage, err := c.Runner.Extract(ctx, prompt, input)
	return text, model, Usage{Input: usage.Input, Output: usage.Output}, err
}
