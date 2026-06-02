// Module crux-agent-chat — version v0.0.1
module crux-agent-chat

go 1.23.0

require (
	crux-agent-harness v0.0.1
	crux-agent-runtime v0.0.1
	crux-ai v0.0.1
	github.com/joho/godotenv v1.5.1
)

require (
	github.com/dlclark/regexp2 v1.10.0 // indirect
	github.com/google/uuid v1.3.0 // indirect
	github.com/pkoukk/tiktoken-go v0.1.6 // indirect
)

replace (
	crux-agent-harness => ../crux-agent-harness
	crux-agent-runtime => ../crux-agent-runtime
	crux-ai => ../crux-ai
)
