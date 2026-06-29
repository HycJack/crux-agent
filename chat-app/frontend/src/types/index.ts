export interface ToolCall {
  id: string;
  name: string;
  arguments: string;
}

export interface ToolExecution {
  id: string;
  name: string;
  result?: string;
  isError?: boolean;
}

export interface Message {
  id: string;
  role: 'user' | 'assistant';
  content: string;
  timestamp: string;
  thinking?: string;
  toolCalls?: ToolCall[];
  toolExecutions?: ToolExecution[];
}

export interface Conversation {
  id: string;
  title: string;
  messages: Message[];
  timestamp: string;
}

export interface Settings {
  provider: 'openai' | 'anthropic';
  apiKey: string;
  baseUrl: string;
  model: string;
  customModel: string;
  workingDir: string;
  ttsEnabled: boolean;
  ttsVoice: string;
  autoLearn?: boolean;
  thinkingLevel?: string;
}