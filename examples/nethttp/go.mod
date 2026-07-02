// net/http middleware example: a request-scoped logger injected into the
// context and pulled back out in any handler. Core srog + sroghttp only.
module github.com/dvislobokov/srog/examples/nethttp

go 1.25.0

require github.com/dvislobokov/srog v0.0.0

require (
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/rs/zerolog v1.35.1 // indirect
	golang.org/x/sys v0.42.0 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.2.1 // indirect
)

replace github.com/dvislobokov/srog => ../..
