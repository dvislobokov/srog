// Direct-to-Elasticsearch example using the opt-in srogelastic sink.
module github.com/dvislobokov/srog/examples/elk

go 1.25.0

require (
	github.com/dvislobokov/srog v1.1.1
	github.com/dvislobokov/srog/srogelastic v0.0.0
)

require (
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/rs/zerolog v1.35.1 // indirect
	golang.org/x/sys v0.42.0 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.2.1 // indirect
)

replace github.com/dvislobokov/srog => ../..

replace github.com/dvislobokov/srog/srogelastic => ../../srogelastic
