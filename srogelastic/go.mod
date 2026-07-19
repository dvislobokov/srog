// Separate, opt-in module: a direct Elasticsearch _bulk sink. It depends only on
// the standard library and core srog — no Elasticsearch client.
module github.com/dvislobokov/srog/srogelastic

go 1.23.0

require github.com/dvislobokov/srog v1.2.0

require (
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/rs/zerolog v1.35.1 // indirect
	golang.org/x/sys v0.30.0 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.2.1 // indirect
)

replace github.com/dvislobokov/srog => ..
