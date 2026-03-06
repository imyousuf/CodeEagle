interface Props {
  label: string;
  description?: string;
  error?: string;
  required?: boolean;
  children: React.ReactNode;
}

const groupStyle: React.CSSProperties = {
  marginBottom: '14px',
};

const labelStyle: React.CSSProperties = {
  display: 'block',
  fontSize: '13px',
  fontWeight: 500,
  color: '#cdd6f4',
  marginBottom: '4px',
};

const requiredStyle: React.CSSProperties = {
  color: '#f38ba8',
  marginLeft: '2px',
};

const descStyle: React.CSSProperties = {
  fontSize: '11px',
  color: '#6c7086',
  marginBottom: '4px',
};

const errorStyle: React.CSSProperties = {
  fontSize: '11px',
  color: '#f38ba8',
  marginTop: '4px',
};

export default function FieldGroup({ label, description, error, required, children }: Props) {
  return (
    <div style={groupStyle}>
      <label style={labelStyle}>
        {label}
        {required && <span style={requiredStyle}>*</span>}
      </label>
      {description && <div style={descStyle}>{description}</div>}
      {children}
      {error && <div style={errorStyle}>{error}</div>}
    </div>
  );
}
