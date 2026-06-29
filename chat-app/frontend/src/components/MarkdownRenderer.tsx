import { memo } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { InlineMath, BlockMath } from 'react-katex';
import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter';
import { oneLight } from 'react-syntax-highlighter/dist/esm/styles/prism';

interface MarkdownRendererProps {
  content: string;
}

function MathNode({ value }: { value: string }) {
  if (value.startsWith('$$') && value.endsWith('$$')) {
    return (
      <div className="math-display">
        <BlockMath math={value.slice(2, -2)} />
      </div>
    );
  }
  return <InlineMath math={value} />;
}

function extractMathContent(text: string): { content: string; isMath: boolean }[] {
  const result: { content: string; isMath: boolean }[] = [];
  const regex = /(\$\$[\s\S]*?\$\$|\$[^$]+\$)/g;
  let lastIndex = 0;
  let match;

  while ((match = regex.exec(text)) !== null) {
    if (match.index > lastIndex) {
      result.push({ content: text.slice(lastIndex, match.index), isMath: false });
    }
    result.push({ content: match[0], isMath: true });
    lastIndex = regex.lastIndex;
  }

  if (lastIndex < text.length) {
    result.push({ content: text.slice(lastIndex), isMath: false });
  }

  return result;
}

// Wrapped with React.memo so completed messages do not re-parse their
// markdown on every stream delta. The active streaming message keeps
// re-rendering (content changes every token), but the rest of the
// conversation is stable.
export default memo(function MarkdownRenderer({ content }: MarkdownRendererProps) {
  return <MarkdownRendererInner content={content} />;
});

function MarkdownRendererInner({ content }: MarkdownRendererProps) {
  const parts = extractMathContent(content);

  return (
    <div className="markdown-content">
      {parts.map((part, index) =>
        part.isMath ? (
          <MathNode key={index} value={part.content} />
        ) : (
          <ReactMarkdown
            key={index}
            remarkPlugins={[remarkGfm]}
            components={{
              code({ node, className, children, ...props }: any) {
                const match = /language-(\w+)/.exec(className || '');
                const text = String(children).replace(/\n$/, '');

                // Determine inline vs block:
                // - If there's a language tag => block
                // - If wrapped in <p> (parent element is paragraph) => inline
                // - If content is short (single word, no newlines) and has no language tag => inline
                // - Otherwise => block
                const parentTag = node?.parent?.tagName;
                const isInParagraph = parentTag === 'p' || parentTag === 'li' || parentTag === 'td' || parentTag === 'th';
                const isShortNoNewline = !text.includes('\n') && text.length < 80;

                if ((!match && isInParagraph) || (!match && isShortNoNewline)) {
                  return (
                    <code className="md-inline-code" {...props}>
                      {children}
                    </code>
                  );
                }

                if (!match && !isInParagraph) {
                  // Block-level code without language — could contain nested backticks.
                  // Detect nested code fences: if the text contains ``` it's likely
                  // a code example showing markdown, not real code to highlight.
                  if (text.includes('```')) {
                    return (
                      <pre className="code-block code-block-raw">
                        <code>{text}</code>
                      </pre>
                    );
                  }
                }

                const language = match ? match[1] : 'text';
                return (
                  <div className="code-block">
                    <div className="code-block-header">
                      <span>{language}</span>
                    </div>
                    <SyntaxHighlighter
                      style={oneLight}
                      language={language}
                      PreTag="pre"
                      customStyle={{
                        margin: 0,
                        padding: '1rem',
                        background: 'transparent',
                        fontSize: '0.875rem',
                        borderBottomLeftRadius: 'var(--radius-md)',
                        borderBottomRightRadius: 'var(--radius-md)',
                      }}
                    >
                      {text}
                    </SyntaxHighlighter>
                  </div>
                );
              },
              hr() {
                return <hr />;
              },
              table({ children }: any) {
                return (
                  <div className="md-table-wrap">
                    <table>{children}</table>
                  </div>
                );
              },
              th({ children }: any) {
                return <th>{children}</th>;
              },
              td({ children }: any) {
                return <td>{children}</td>;
              },
              blockquote({ children }: any) {
                return <blockquote>{children}</blockquote>;
              },
              a({ href, children }: any) {
                return (
                  <a href={href} target="_blank" rel="noopener noreferrer">
                    {children}
                  </a>
                );
              },
              p({ children }: any) {
                return <p>{children}</p>;
              },
              ul({ children }: any) {
                return <ul>{children}</ul>;
              },
              ol({ children }: any) {
                return <ol>{children}</ol>;
              },
              li({ children }: any) {
                return <li>{children}</li>;
              },
              h1({ children }: any) {
                return <h1>{children}</h1>;
              },
              h2({ children }: any) {
                return <h2>{children}</h2>;
              },
              h3({ children }: any) {
                return <h3>{children}</h3>;
              },
              h4({ children }: any) {
                return <h4>{children}</h4>;
              },
            }}
          >
            {part.content}
          </ReactMarkdown>
        ),
      )}
    </div>
  );
}