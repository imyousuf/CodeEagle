import FieldGroup from './FieldGroup';
import PathPicker from './PathPicker';
import ChipList from './ChipList';
import SectionHeader from './SectionHeader';
import type { DocsDTO } from '../../types';

interface Props {
  docs: DocsDTO;
  onUpdate: (field: string, value: unknown) => void;
  onUpdateFaces: (field: string, value: unknown) => void;
  onAddExtension: (ext: string) => void;
  onRemoveExtension: (index: number) => void;
  onBrowseDir: (title: string) => Promise<string>;
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

const selectStyle: React.CSSProperties = {
  ...inputStyle,
};

const toggleStyle: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: '8px',
  fontSize: '13px',
  color: '#cdd6f4',
  cursor: 'pointer',
  marginBottom: '8px',
};

const numInputStyle: React.CSSProperties = {
  ...inputStyle,
  width: '120px',
};

export default function DocumentsSection({
  docs,
  onUpdate,
  onUpdateFaces,
  onAddExtension,
  onRemoveExtension,
  onBrowseDir,
}: Props) {
  return (
    <div>
      <FieldGroup label="Provider" description="LLM provider for document analysis (images, PDFs, etc).">
        <select
          style={selectStyle}
          value={docs.provider}
          onChange={e => onUpdate('provider', e.target.value)}
        >
          <option value="">Auto-detect</option>
          <option value="ollama">Ollama</option>
          <option value="vertex-ai">Vertex AI</option>
        </select>
      </FieldGroup>

      <FieldGroup label="Model" description="Model for document understanding.">
        <input
          type="text"
          style={inputStyle}
          value={docs.model}
          onChange={e => onUpdate('model', e.target.value)}
          placeholder="(auto)"
        />
      </FieldGroup>

      <div style={{ display: 'flex', gap: '12px' }}>
        <FieldGroup label="Max Image Resolution" description="Longest edge in pixels.">
          <input
            type="number"
            style={numInputStyle}
            value={docs.max_image_resolution}
            onChange={e => onUpdate('max_image_resolution', parseInt(e.target.value) || 0)}
            min={256}
            max={4096}
          />
        </FieldGroup>
        <FieldGroup label="Context Window" description="Ollama context size.">
          <input
            type="number"
            style={numInputStyle}
            value={docs.context_window}
            onChange={e => onUpdate('context_window', parseInt(e.target.value) || 0)}
            min={1024}
          />
        </FieldGroup>
      </div>

      <label style={toggleStyle}>
        <input
          type="checkbox"
          checked={docs.disable_thinking}
          onChange={e => onUpdate('disable_thinking', e.target.checked)}
        />
        Disable Thinking (saves tokens, may reduce quality)
      </label>

      <FieldGroup label="Exclude Extensions" description="File extensions to skip when indexing documents.">
        <ChipList
          items={docs.exclude_extensions || []}
          onAdd={onAddExtension}
          onRemove={onRemoveExtension}
          placeholder=".lock"
        />
      </FieldGroup>

      {/* Face Detection */}
      <SectionHeader title="Face Detection" description="OpenCV-based face and object detection in images." defaultExpanded={false}>
        <label style={toggleStyle}>
          <input
            type="checkbox"
            checked={docs.faces.enabled}
            onChange={e => onUpdateFaces('enabled', e.target.checked)}
          />
          Enable face and object detection
        </label>

        {docs.faces.enabled && (
          <>
            <FieldGroup label="Model Directory" description="Directory for ONNX model files.">
              <PathPicker
                value={docs.faces.model_dir}
                onChange={v => onUpdateFaces('model_dir', v)}
                onBrowse={async () => {
                  const d = await onBrowseDir('Select model directory');
                  if (d) onUpdateFaces('model_dir', d);
                }}
                placeholder="~/.codeeagle/models/"
              />
            </FieldGroup>

            <div style={{ display: 'flex', gap: '12px', flexWrap: 'wrap' }}>
              <FieldGroup label="Min Face Size (px)">
                <input
                  type="number"
                  style={numInputStyle}
                  value={docs.faces.min_face_size}
                  onChange={e => onUpdateFaces('min_face_size', parseInt(e.target.value) || 0)}
                  min={10}
                />
              </FieldGroup>
              <FieldGroup label="Similarity Threshold">
                <input
                  type="number"
                  style={numInputStyle}
                  value={docs.faces.similarity_threshold}
                  onChange={e => onUpdateFaces('similarity_threshold', parseFloat(e.target.value) || 0)}
                  step={0.05}
                  min={0}
                  max={1}
                />
              </FieldGroup>
              <FieldGroup label="Confidence Threshold">
                <input
                  type="number"
                  style={numInputStyle}
                  value={docs.faces.confidence_threshold}
                  onChange={e => onUpdateFaces('confidence_threshold', parseFloat(e.target.value) || 0)}
                  step={0.05}
                  min={0}
                  max={1}
                />
              </FieldGroup>
            </div>

            <label style={toggleStyle}>
              <input
                type="checkbox"
                checked={docs.faces.object_detection}
                onChange={e => onUpdateFaces('object_detection', e.target.checked)}
              />
              Enable object detection (YOLO labels)
            </label>
          </>
        )}
      </SectionHeader>
    </div>
  );
}
