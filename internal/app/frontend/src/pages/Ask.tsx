import { useRef, useEffect } from 'react';
import AgentSelector from '../components/AgentSelector';
import ChatMessage from '../components/ChatMessage';
import ChatInput from '../components/ChatInput';
import { useAgent } from '../hooks/useAgent';

const containerStyle: React.CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  height: '100%',
  maxWidth: '900px',
  margin: '0 auto',
};

const messagesStyle: React.CSSProperties = {
  flex: 1,
  overflow: 'auto',
  display: 'flex',
  flexDirection: 'column',
  gap: '4px',
  padding: '8px 0',
};

const emptyStyle: React.CSSProperties = {
  flex: 1,
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  color: '#a6adc8',
  fontSize: '14px',
  textAlign: 'center',
};

const errorStyle: React.CSSProperties = {
  padding: '12px 16px',
  background: '#302030',
  border: '1px solid #f38ba8',
  borderRadius: '6px',
  color: '#f38ba8',
  fontSize: '13px',
  marginBottom: '8px',
};

const spinnerStyle: React.CSSProperties = {
  padding: '12px 16px',
  color: '#89b4fa',
  fontSize: '13px',
  fontStyle: 'italic',
};

export default function Ask() {
  const {
    agents,
    selectedAgent,
    setSelectedAgent,
    messages,
    loading,
    error,
    sendMessage,
    clearMessages,
  } = useAgent();

  const messagesEndRef = useRef<HTMLDivElement>(null);

  // Auto-scroll to bottom on new messages.
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [messages, loading]);

  return (
    <div style={containerStyle}>
      <AgentSelector
        agents={agents}
        selected={selectedAgent}
        onChange={setSelectedAgent}
        onClear={clearMessages}
      />

      {error && <div style={errorStyle}>{error}</div>}

      {messages.length === 0 && !loading ? (
        <div style={emptyStyle}>
          <div>
            <p>Select an agent and ask a question about your codebase.</p>
            <p style={{ marginTop: '8px', fontSize: '12px' }}>
              The planner analyzes impact and dependencies. The designer reviews architecture.
              <br />
              The reviewer checks code quality. The asker handles general questions.
            </p>
          </div>
        </div>
      ) : (
        <div style={messagesStyle}>
          {messages.map((msg, i) => (
            <ChatMessage key={i} message={msg} />
          ))}
          {loading && (
            <div style={spinnerStyle}>Thinking...</div>
          )}
          <div ref={messagesEndRef} />
        </div>
      )}

      <ChatInput onSend={sendMessage} disabled={loading} />
    </div>
  );
}
