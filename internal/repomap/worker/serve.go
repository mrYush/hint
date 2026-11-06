package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/mrYush/hint/internal/repomap/extract"
)

// Serve processes bounded newline-delimited extraction requests sequentially.
// Its input carries source bytes, never permission to open a source path.
// The supervising parent must close the pipes/kill the process on cancellation;
// an arbitrary io.Reader cannot be preempted by a context alone.
func Serve(ctx context.Context, input io.Reader, output io.Writer) (resultErr error) {
	engine := &extract.Engine{}
	defer func() { resultErr = errors.Join(resultErr, engine.Close()) }()
	reader := bufio.NewReaderSize(input, 32<<10)
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("serve extraction: %w", err)
		}
		line, err := readLine(reader, maxRequestBytes)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read extraction request: %w", err)
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			return fmt.Errorf("decode extraction request: %w", err)
		}
		if len(req.Source.Path) > 4096 {
			return errors.New("extraction path exceeds limit")
		}
		result, extractErr := engine.Extract(ctx, req.Source, req.Limits)
		resp := response{Result: result}
		if extractErr != nil {
			// Do not echo source-bearing parser diagnostics over the protocol.
			resp.Error = "extraction failed"
		}
		data, err := json.Marshal(resp)
		if err != nil {
			return fmt.Errorf("encode extraction response: %w", err)
		}
		if len(data)+1 > maxResponseBytes {
			return errors.New("extraction response exceeds limit")
		}
		frame := append(data, '\n')
		n, err := output.Write(frame)
		if err != nil {
			return fmt.Errorf("write extraction response: %w", err)
		}
		if n != len(frame) {
			return fmt.Errorf("write extraction response: %w", io.ErrShortWrite)
		}
	}
}
