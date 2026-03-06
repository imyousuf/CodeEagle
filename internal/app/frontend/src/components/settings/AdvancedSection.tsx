import FieldGroup from './FieldGroup';
import ChipList from './ChipList';

interface Props {
  excludePatterns: string[];
  onAddPattern: (pattern: string) => void;
  onRemovePattern: (index: number) => void;
  graphStorage: string;
}

const readOnlyStyle: React.CSSProperties = {
  padding: '6px 10px',
  fontSize: '13px',
  background: '#181825',
  border: '1px solid #313244',
  borderRadius: '6px',
  color: '#6c7086',
};

export default function AdvancedSection({
  excludePatterns,
  onAddPattern,
  onRemovePattern,
  graphStorage,
}: Props) {
  return (
    <div>
      <FieldGroup
        label="Watch Exclude Patterns"
        description="Glob patterns for files/directories to ignore during indexing."
      >
        <ChipList
          items={excludePatterns}
          onAdd={onAddPattern}
          onRemove={onRemovePattern}
          placeholder="**/node_modules/**"
        />
      </FieldGroup>

      <FieldGroup label="Graph Storage" description="Storage backend (read-only).">
        <div style={readOnlyStyle}>{graphStorage || 'embedded'}</div>
      </FieldGroup>
    </div>
  );
}
