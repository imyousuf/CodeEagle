import FieldGroup from './FieldGroup';

interface Props {
  name: string;
  onChange: (name: string) => void;
}

const inputStyle: React.CSSProperties = {
  width: '100%',
  padding: '6px 10px',
  fontSize: '13px',
  background: '#313244',
  border: '1px solid #45475a',
  borderRadius: '6px',
  color: '#cdd6f4',
  outline: 'none',
  boxSizing: 'border-box',
};

export default function ProjectSection({ name, onChange }: Props) {
  return (
    <FieldGroup label="Project Name" required>
      <input
        type="text"
        style={inputStyle}
        value={name}
        onChange={e => onChange(e.target.value)}
        placeholder="My Project"
      />
    </FieldGroup>
  );
}
