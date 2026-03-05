import { useState, useRef, useEffect } from 'react';

const containerStyle: React.CSSProperties = {
  display: 'flex',
  gap: '8px',
  padding: '12px 0',
  borderTop: '1px solid #45475a',
};

const textareaStyle: React.CSSProperties = {
  flex: 1,
  padding: '10px 14px',
  fontSize: '14px',
  background: '#313244',
  border: '1px solid #45475a',
  borderRadius: '6px',
  color: '#cdd6f4',
  outline: 'none',
  resize: 'none',
  minHeight: '42px',
  maxHeight: '120px',
  fontFamily: 'inherit',
  lineHeight: 1.4,
};

const sendBtnStyle: React.CSSProperties = {
  padding: '10px 20px',
  fontSize: '14px',
  fontWeight: 600,
  background: '#89b4fa',
  color: '#1e1e2e',
  border: 'none',
  borderRadius: '6px',
  cursor: 'pointer',
  alignSelf: 'flex-end',
};

interface Props {
  onSend: (message: string) => void;
  disabled: boolean;
}

export default function ChatInput({ onSend, disabled }: Props) {
  const [text, setText] = useState('');
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    if (textareaRef.current) {
      textareaRef.current.style.height = 'auto';
      textareaRef.current.style.height = Math.min(textareaRef.current.scrollHeight, 120) + 'px';
    }
  }, [text]);

  const handleSend = () => {
    if (text.trim() && !disabled) {
      onSend(text.trim());
      setText('');
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  return (
    <div style={containerStyle}>
      <textarea
        ref={textareaRef}
        value={text}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={handleKeyDown}
        placeholder="Ask a question about your codebase..."
        style={textareaStyle}
        rows={1}
        disabled={disabled}
      />
      <button
        onClick={handleSend}
        disabled={disabled || !text.trim()}
        style={{
          ...sendBtnStyle,
          opacity: disabled || !text.trim() ? 0.5 : 1,
        }}
      >
        Send
      </button>
    </div>
  );
}
