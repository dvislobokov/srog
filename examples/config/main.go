// Command config builds a logger from a declarative srog.Config (here inline
// JSON; in production load it from a file with srog.LoadConfigFile). It fans out
// to a human console on stderr and an ECS-formatted, rotating file for shipping
// to Elasticsearch.
//
//	go run .
package main

import (
	"strings"

	"github.com/dvislobokov/srog"
)

const configJSON = `{
  "level": "debug",
  "caller": true,
  "stackTrace": true,
  "timeFormat": "rfc3339",
  "sinks": [
    { "type": "console", "target": "stderr", "level": "debug" },
    { "type": "file", "path": "./config-app.log", "format": "ecs", "level": "information",
      "rotation": { "maxSizeMB": 50, "maxBackups": 5, "compress": true, "every": "daily" } }
  ]
}`

func main() {
	cfg, err := srog.LoadConfig(strings.NewReader(configJSON))
	if err != nil {
		panic(err)
	}
	log, err := cfg.Build()
	if err != nil {
		panic(err)
	}
	defer log.Close()

	log.Debug("debug lines show on the console sink only")
	log.Information("configured from JSON: console(stderr) + rotating ECS file")
	log.Warning("this reaches both sinks at/above information")
}
