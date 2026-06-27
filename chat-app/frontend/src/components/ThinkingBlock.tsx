import { useState } from 'react';
import MarkdownRenderer from './MarkdownRenderer';
import { ChevronDownOutlined, ChevronUpOutlined } from '../icons';

interface ThinkingBlockProps {
  content: string;
  defaultExpanded?: boolean;
}

export default function ThinkingBlock({ content, defaultExpanded = true }: ThinkingBlockProps) {
  const [expanded, setExpanded] = useState(defaultExpanded);
  if (!content || content.trim() === '') return null;

  return (
    <div className="think-block">
      <button className="think-header" onClick={() => setExpanded(!expanded)}>
        <span className="think-title">
          <span className="think-dot" />
          Thinking
        </span>
        {expanded ? <ChevronUpOutlined /> : <ChevronDownOutlined />}
      </button>
      {expanded && (
        <div className="think-body">
          <MarkdownRenderer content={content} />
        </div>
      )}
    </div>
  );
}