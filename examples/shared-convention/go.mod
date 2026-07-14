// Shared-convention example: one platformlog package pins the correlation-id
// field/header/metadata names so every service wires logging identically and
// handlers never reference the field. HTTP + gRPC, with cross-service propagation.
module github.com/dvislobokov/srog/examples/shared-convention

go 1.25.0

require (
	github.com/dvislobokov/srog v1.1.1
	github.com/dvislobokov/srog/sroggrpc v0.0.0
	google.golang.org/grpc v1.81.1
)

require (
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/rs/zerolog v1.35.1 // indirect
	golang.org/x/net v0.51.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260226221140-a57be14db171 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.2.1 // indirect
)

replace github.com/dvislobokov/srog => ../..

replace github.com/dvislobokov/srog/sroggrpc => ../../sroggrpc
