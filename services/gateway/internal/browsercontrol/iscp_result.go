package browsercontrol

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

func (c *ISCPControllerClient) fetchResult(ctx context.Context, intent ControllerIntent, descriptor *controllerResultDescriptor, limits ControllerWireLimits) ([]byte, error) {
	if descriptor == nil || !controllerID.MatchString(descriptor.ResultID) || descriptor.TotalBytes < 1 || descriptor.TotalBytes > maxControllerExecutionResponseBytes ||
		!controllerHash.MatchString(descriptor.SHA256) || !time.Now().Before(descriptor.ExpiresAt) || descriptor.ExpiresAt.After(time.Now().Add(61*time.Second)) {
		return nil, newError(CodeResultUnavailable, false, nil)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	payload := make([]byte, descriptor.TotalBytes)
	var workers sync.WaitGroup
	var once sync.Once
	var failed error
	fail := func(err error) { once.Do(func() { failed = err; cancel() }) }
	for index := 0; index < limits.ChunkConcurrency; index++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			for offset := index * limits.ChunkBytes; offset < len(payload); offset += limits.ChunkConcurrency * limits.ChunkBytes {
				if ctx.Err() != nil {
					fail(ctx.Err())
					return
				}
				length := min(limits.ChunkBytes, len(payload)-offset)
				input, err := json.Marshal(map[string]any{"schema_version": 1, "binding": c.binding, "command_id": intent.CommandID,
					"result_id": descriptor.ResultID, "offset": offset, "length": length})
				if err != nil {
					fail(err)
					return
				}
				raw, err := c.transport.ControllerCall(ctx, "browser.controller.chunk", input)
				if err != nil {
					fail(err)
					return
				}
				var chunk struct {
					SchemaVersion int    `json:"schema_version"`
					CommandID     string `json:"command_id"`
					ResultID      string `json:"result_id"`
					Offset        int    `json:"offset"`
					TotalBytes    int    `json:"total_bytes"`
					Data          string `json:"data_base64"`
				}
				valid := len(raw) <= limits.WireBytes && strictControllerJSON(raw, &chunk) == nil && chunk.SchemaVersion == 1 && chunk.CommandID == intent.CommandID && chunk.ResultID == descriptor.ResultID && chunk.Offset == offset && chunk.TotalBytes == descriptor.TotalBytes
				zero(raw)
				if !valid {
					fail(errors.New("Controller chunk identity differs"))
					return
				}
				decoded, err := base64.StdEncoding.Strict().DecodeString(chunk.Data)
				if err != nil || len(decoded) != length {
					zero(decoded)
					fail(errors.New("Controller chunk size differs"))
					return
				}
				copy(payload[offset:offset+length], decoded)
				zero(decoded)
			}
		}(index)
	}
	workers.Wait()
	if failed == nil {
		sum := sha256.Sum256(payload)
		if hex.EncodeToString(sum[:]) != descriptor.SHA256 || !json.Valid(payload) {
			failed = errors.New("Controller result digest differs")
		}
	}
	if failed != nil {
		zero(payload)
		return nil, newError(CodeResultUnavailable, false, failed)
	}
	return payload, nil
}
