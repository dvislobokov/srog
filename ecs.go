package srog

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/rs/zerolog"
)

// ecsVersion is the Elastic Common Schema version the ECS sink advertises.
const ecsVersion = "8.11.0"

// ecsWriter rewrites each zerolog JSON line into Elastic Common Schema field
// names so events land in a standard Elasticsearch index and render in Kibana
// without any Logstash/ingest mapping. It remaps the well-known fields
// (time->@timestamp, level->log.level, error->error.message, stack->
// error.stack_trace, caller->log.origin.file.*), injects ecs.version, and passes
// template fields through unchanged.
//
// It decodes with json.Number so integer fields keep full precision across the
// re-encode.
type ecsWriter struct {
	out io.Writer
}

func (w ecsWriter) Write(p []byte) (int, error) {
	dec := json.NewDecoder(bytes.NewReader(p))
	dec.UseNumber()
	var evt map[string]any
	if err := dec.Decode(&evt); err != nil {
		return w.out.Write(p) // not JSON we understand — pass through
	}

	out := make(map[string]any, len(evt)+2)
	out["ecs.version"] = ecsVersion
	for k, v := range evt {
		switch k {
		case zerolog.TimestampFieldName:
			out["@timestamp"] = v
		case zerolog.LevelFieldName:
			out["log.level"] = v
		case zerolog.MessageFieldName:
			out["message"] = v
		case zerolog.ErrorFieldName:
			out["error.message"] = v
		case stackFieldName:
			out["error.stack_trace"] = v
		case zerolog.CallerFieldName:
			// "file:line" -> ECS log.origin fields.
			if s, ok := v.(string); ok {
				if i := strings.LastIndexByte(s, ':'); i >= 0 {
					out["log.origin.file.name"] = s[:i]
					if n, err := json.Number(s[i+1:]).Int64(); err == nil {
						out["log.origin.file.line"] = n
					} else {
						out["log.origin.file.name"] = s
					}
				} else {
					out["log.origin.file.name"] = s
				}
			} else {
				out["log.origin.file.name"] = v
			}
		case "@mt":
			out["message_template.text"] = v
		default:
			out[k] = v
		}
	}

	b, err := json.Marshal(out)
	if err != nil {
		return w.out.Write(p)
	}
	b = append(b, '\n')
	if _, err := w.out.Write(b); err != nil {
		return 0, err
	}
	// Report the input length so MultiLevelWriter does not see a short write.
	return len(p), nil
}
