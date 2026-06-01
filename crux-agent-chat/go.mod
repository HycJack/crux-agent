module crux-agent-chat

go 1.23.0

require (
	crux-agent-runtime v0.0.0
	crux-ai v0.0.0
	github.com/joho/godotenv v1.5.1
)

replace (
	crux-agent-runtime => ../crux-agent-runtime
	crux-ai => ../crux-ai
)
