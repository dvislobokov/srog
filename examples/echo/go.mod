// Separate module so the echo dependency stays out of the srog library's go.mod.
// Run from this directory: `go mod tidy && go run .`
module github.com/dvislobokov/srog/examples/echo

go 1.25.0

require (
	github.com/dvislobokov/srog v0.0.0
	github.com/dvislobokov/srog/srogecho v0.0.0
	github.com/labstack/echo/v4 v4.12.0
)

require (
	github.com/labstack/gommon v0.4.2 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/rs/zerolog v1.35.1 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasttemplate v1.2.2 // indirect
	golang.org/x/crypto v0.48.0 // indirect
	golang.org/x/net v0.51.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.2.1 // indirect
)

replace github.com/dvislobokov/srog => ../..

replace github.com/dvislobokov/srog/srogecho => ../../srogecho
