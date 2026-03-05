import Markdown from 'react-markdown';
import type { ChatMessage as Message } from '../types';

const userStyle: React.CSSProperties = {
  padding: '10px 14px',
  background: '#45475a',
  borderRadius: '8px 8px 2px 8px',
  marginBottom: '12px',
  maxWidth: '80%',
  alignSelf: 'flex-end',
  fontSize: '14px',
  color: '#cdd6f4',
};

const assistantStyle: React.CSSProperties = {
  padding: '10px 14px',
  background: '#313244',
  border: '1px solid #45475a',
  borderRadius: '2px 8px 8px 8px',
  marginBottom: '12px',
  maxWidth: '90%',
  alignSelf: 'flex-start',
  fontSize: '14px',
  color: '#cdd6f4',
  lineHeight: 1.6,
};

const labelStyle: React.CSSProperties = {
  fontSize: '11px',
  fontWeight: 600,
  color: '#a6adc8',
  marginBottom: '4px',
  textTransform: 'uppercase',
};

interface Props {
  message: Message;
}

export default function ChatMessage({ message }: Props) {
  const isUser = message.role === 'user';

  return (
    <div style={isUser ? userStyle : assistantStyle}>
      <div style={labelStyle}>
        {isUser ? 'You' : message.agent || 'Assistant'}
      </div>
      {isUser ? (
        <div>{message.content}</div>
      ) : (
        <Markdown>{message.content}</Markdown>
      )}
    </div>
  );
}
