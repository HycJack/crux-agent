import { useState } from 'react';
import type { ToolCall, ToolExecution } from '../types';
import { ChevronDownOutlined, ChevronUpOutlined, CodeOutlined, WrenchOutlined } from '../icons';

interface ToolCallBlockProps {
  toolCalls: ToolCall[];
  toolExecutions?: ToolExecution[];
  defaultExpanded?: boolean;
}

export default function ToolCallBlock({ toolCalls, toolExecutions, defaultExpanded = true }: ToolCallBlockProps) {
  const [expanded, setExpanded] = useState(defaultExpanded);
  if (!toolCalls || toolCalls.length === 0) return null;

  return (
    <div className="tools-block">
      <button className="tools-header" onClick={() => setExpanded(!expanded)}>
        <span className="tools-title">
          <WrenchOutlined />
          <span>Tools ({toolCalls.length})</span>
        </span>
        {expanded ? <ChevronUpOutlined /> : <ChevronDownOutlined />}
      </button>
      {expanded && (
        <div className="tools-body">
          {toolCalls.map((tc, idx) => {
            const exec = toolExecutions?.find((e) => e.id === tc.id);
            const argsText = (() => {
              try {
                return JSON.stringify(JSON.parse(tc.arguments || '{}'), null, 2);
              } catch {
                return tc.arguments || '';
              }
            })();
            return (
              <div key={tc.id || idx} className="tool-row">
                <div className="tool-row-header">
                  <CodeOutlined />
                  <span className="tool-name">{tc.name}</span>
                  {exec?.isError && <span className="tool-badge error">error</span>}
                  {exec && !exec.isError && exec.result && <span className="tool-badge ok">done</span>}
                </div>
                {argsText && (
                  <pre className="tool-args">
                    <code>{argsText}</code>
                  </pre>
                )}
                {exec?.result && (
                  <pre className={`tool-result ${exec.isError ? 'is-error' : ''}`}>
                    <code>{exec.result}</code>
                  </pre>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}