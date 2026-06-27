# Chat App

## About

This is a Wails-based AI chat application with React frontend. It provides a ChatGPT-style interface with streaming responses, markdown rendering, and text-to-speech capabilities.

## Features

- **Streaming Responses**: Real-time streaming output from AI models
- **Markdown Support**: Full markdown rendering including code blocks with syntax highlighting
- **LaTeX Support**: Math formula rendering using KaTeX
- **Text-to-Speech**: Manual play/stop audio playback of AI responses
- **Tool Calls**: Display tool call information with expandable/collapsible sections
- **Thinking Content**: Display AI thinking process with expandable/collapsible sections
- **Settings Panel**: Configure API key, base URL, model selection, and provider type (OpenAI/Anthropic)
- **Conversation History**: Save and manage multiple conversations

## Configuration

1. Open the settings panel (gear icon in sidebar)
2. Configure:
   - Provider: OpenAI or Anthropic
   - API Key: Your API key
   - Base URL: The API endpoint URL
   - Model: Select a model from the dropdown or enter manually

## Live Development

To run in live development mode, run `wails dev` in the project directory. This will run a Vite development server that will provide very fast hot reload of your frontend changes. If you want to develop in a browser and have access to your Go methods, there is also a dev server that runs on http://localhost:34115. Connect to this in your browser, and you can call your Go code from devtools.

## Building

To build a redistributable, production mode package, use `wails build`.

## Technology Stack

- **Wails**: Cross-platform desktop app framework
- **React 18**: Frontend UI framework
- **TypeScript**: Type-safe development
- **TailwindCSS 3**: CSS framework
- **Lucide React**: Icon library
- **React Markdown**: Markdown rendering
- **react-syntax-highlighter**: Code syntax highlighting
- **KaTeX**: Math formula rendering
