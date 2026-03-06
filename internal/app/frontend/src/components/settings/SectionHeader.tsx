import { useState } from 'react';

interface Props {
  title: string;
  description?: string;
  defaultExpanded?: boolean;
  children: React.ReactNode;
}

const sectionStyle: React.CSSProperties = {
  marginBottom: '16px',
  background: '#1e1e2e',
  borderRadius: '8px',
  border: '1px solid #313244',
  overflow: 'hidden',
};

const headerStyle: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'space-between',
  padding: '12px 16px',
  cursor: 'pointer',
  userSelect: 'none',
};

const titleStyle: React.CSSProperties = {
  fontSize: '15px',
  fontWeight: 600,
  color: '#cdd6f4',
};

const descStyle: React.CSSProperties = {
  fontSize: '12px',
  color: '#6c7086',
  marginTop: '2px',
};

const chevronStyle = (expanded: boolean): React.CSSProperties => ({
  fontSize: '12px',
  color: '#6c7086',
  transition: 'transform 0.2s',
  transform: expanded ? 'rotate(180deg)' : 'rotate(0deg)',
});

const contentStyle: React.CSSProperties = {
  padding: '0 16px 16px',
};

export default function SectionHeader({
  title,
  description,
  defaultExpanded = true,
  children,
}: Props) {
  const [expanded, setExpanded] = useState(defaultExpanded);

  return (
    <div style={sectionStyle}>
      <div style={headerStyle} onClick={() => setExpanded(e => !e)}>
        <div>
          <div style={titleStyle}>{title}</div>
          {description && <div style={descStyle}>{description}</div>}
        </div>
        <span style={chevronStyle(expanded)}>&#9660;</span>
      </div>
      {expanded && <div style={contentStyle}>{children}</div>}
    </div>
  );
}
