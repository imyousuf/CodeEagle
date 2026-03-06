import FieldGroup from './FieldGroup';

interface Props {
  selected: string[];
  allLanguages: string[];
  onChange: (languages: string[]) => void;
  onDetect: () => void;
}

const gridStyle: React.CSSProperties = {
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fill, minmax(140px, 1fr))',
  gap: '6px',
  marginBottom: '10px',
};

const checkStyle: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: '6px',
  fontSize: '13px',
  color: '#cdd6f4',
  cursor: 'pointer',
};

const detectBtnStyle: React.CSSProperties = {
  padding: '6px 14px',
  fontSize: '12px',
  background: '#45475a',
  border: 'none',
  borderRadius: '6px',
  color: '#89b4fa',
  cursor: 'pointer',
};

// Friendly display names for languages.
const langLabels: Record<string, string> = {
  go: 'Go',
  python: 'Python',
  typescript: 'TypeScript',
  javascript: 'JavaScript',
  java: 'Java',
  rust: 'Rust',
  csharp: 'C#',
  ruby: 'Ruby',
  html: 'HTML',
  markdown: 'Markdown',
  makefile: 'Makefile',
  shell: 'Shell',
  terraform: 'Terraform',
  yaml: 'YAML',
};

export default function LanguagesSection({ selected, allLanguages, onChange, onDetect }: Props) {
  const selectedSet = new Set(selected);

  const toggle = (lang: string) => {
    const next = new Set(selectedSet);
    if (next.has(lang)) {
      next.delete(lang);
    } else {
      next.add(lang);
    }
    onChange(Array.from(next));
  };

  return (
    <FieldGroup
      label="Languages"
      description="Which programming languages should CodeEagle parse?"
    >
      <div style={gridStyle}>
        {allLanguages.map(lang => (
          <label key={lang} style={checkStyle}>
            <input
              type="checkbox"
              checked={selectedSet.has(lang)}
              onChange={() => toggle(lang)}
            />
            {langLabels[lang] || lang}
          </label>
        ))}
      </div>
      <button type="button" style={detectBtnStyle} onClick={onDetect}>
        Detect from repositories
      </button>
    </FieldGroup>
  );
}
