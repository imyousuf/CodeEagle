import { useRef, useEffect } from 'react';
import AgentSelector from '../components/AgentSelector';
import ChatMessage from '../components/ChatMessage';
import ChatInput from '../components/ChatInput';
import { useAgent } from '../hooks/useAgent';
import type { AppStatus } from '../types';

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

const bannerStyle: React.CSSProperties = {
  padding: '16px 20px',
  background: '#1e1e2e',
  border: '1px solid #45475a',
  borderRadius: '8px',
  marginBottom: '12px',
};

const bannerTitle: React.CSSProperties = {
  fontSize: '14px',
  fontWeight: 600,
  color: '#f9e2af',
  marginBottom: '8px',
};

const bannerText: React.CSSProperties = {
  fontSize: '13px',
  color: '#a6adc8',
  lineHeight: '1.5',
};

const codeInline: React.CSSProperties = {
  background: '#313244',
  padding: '2px 6px',
  borderRadius: '4px',
  fontFamily: 'monospace',
  fontSize: '12px',
  color: '#89b4fa',
};

interface Props {
  status: AppStatus | null;
}

export default function Ask({ status }: Props) {
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

  const missingResources: string[] = [];
  if (status && !status.llm_ready) missingResources.push('LLM');
  if (status && !status.graph_ready) missingResources.push('knowledge graph');

  return (
    <div style={containerStyle}>
      <AgentSelector
        agents={agents}
        selected={selectedAgent}
        onChange={setSelectedAgent}
        onClear={clearMessages}
      />

      {missingResources.length > 0 && (
        <div style={bannerStyle}>
          <div style={bannerTitle}>
            {missingResources.join(' and ')} not available
          </div>
          <div style={bannerText}>
            {!status!.graph_ready && (
              <div style={{ marginBottom: status!.llm_ready ? 0 : '6px' }}>
                Run <span style={codeInline}>codeeagle sync</span> to build the knowledge graph.
              </div>
            )}
            {!status!.llm_ready && (
              <div>
                Configure an LLM provider in{' '}
                <span style={codeInline}>Settings</span> or check your{' '}
                <span style={codeInline}>.CodeEagle/config.yaml</span> to enable AI agents.
              </div>
            )}
            <div style={{ marginTop: '6px', fontSize: '12px', color: '#6c7086' }}>
              You can still type questions — they will be sent once resources are available.
            </div>
          </div>
        </div>
      )}

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
