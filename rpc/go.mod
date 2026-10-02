module somewear/rpc

go 1.22

require (
	golang.org/x/term v0.21.0
	google.golang.org/protobuf v1.34.2
	somewear/sensors v0.0.0
)

replace somewear/sensors => ../sensors

require golang.org/x/sys v0.21.0 // indirect
