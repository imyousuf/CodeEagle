import FieldGroup from './FieldGroup';
import PathPicker from './PathPicker';
import StatusDot from './StatusDot';
import SectionHeader from './SectionHeader';
import type { AgentsDTO, DetectionResult, ServiceStatus } from '../../types';

interface Props {
  agents: AgentsDTO;
  detection: DetectionResult | null;
  testResults: Record<string, ServiceStatus>;
  onUpdate: (field: string, value: unknown) => void;
  onTestLLM: () => void;
  onTestOllama: (baseURL?: string) => void;
  onBrowseFile: (title: string, filter: string) => Promise<string>;
}

const selectStyle: React.CSSProperties = {
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

const inputStyle: React.CSSProperties = {
  ...selectStyle,
};

const rowStyle: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: '8px',
  marginBottom: '10px',
};

const testBtnStyle: React.CSSProperties = {
  padding: '6px 14px',
  fontSize: '12px',
  background: '#45475a',
  border: 'none',
  borderRadius: '6px',
  color: '#89b4fa',
  cursor: 'pointer',
  whiteSpace: 'nowrap',
};

const noteStyle: React.CSSProperties = {
  fontSize: '12px',
  color: '#6c7086',
  padding: '8px 12px',
  background: '#181825',
  borderRadius: '6px',
  marginBottom: '10px',
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

export default function AISection({
  agents,
  detection,
  testResults,
  onUpdate,
  onTestLLM,
  onTestOllama,
  onBrowseFile,
}: Props) {
  const llmTest = testResults['llm'];
  const ollamaTest = testResults['ollama'];

  const llmStatus = llmTest
    ? llmTest.available ? 'ok' : 'error'
    : 'unknown';

  return (
    <div>
      {/* LLM Provider */}
      <FieldGroup label="LLM Provider" description="Choose the AI service for code analysis.">
        <div style={rowStyle}>
          <select
            style={{ ...selectStyle, flex: 1 }}
            value={agents.llm_provider}
            onChange={e => onUpdate('llm_provider', e.target.value)}
          >
            <option value="claude-cli">Claude Code CLI</option>
            <option value="anthropic">Anthropic API</option>
            <option value="vertex-ai">Vertex AI (Google Cloud)</option>
            <option value="ollama">Ollama (Local)</option>
          </select>
          <StatusDot status={llmStatus} />
          <button type="button" style={testBtnStyle} onClick={onTestLLM}>
            Test
          </button>
        </div>
        {llmTest && (
          <div style={{ ...noteStyle, color: llmTest.available ? '#a6e3a1' : '#f38ba8' }}>
            {llmTest.message}
          </div>
        )}
      </FieldGroup>

      {/* Provider-specific options */}
      {agents.llm_provider === 'claude-cli' && (
        <div style={noteStyle}>
          Uses your installed Claude Code CLI. No API key needed.
          {detection?.claude_cli && <span style={{ color: '#a6e3a1' }}> (Detected)</span>}
        </div>
      )}

      {agents.llm_provider === 'anthropic' && (
        <div style={noteStyle}>
          Set ANTHROPIC_API_KEY in .CodeEagle/.env or as an environment variable.
          {detection?.anthropic_key && <span style={{ color: '#a6e3a1' }}> (Key found)</span>}
        </div>
      )}

      {agents.llm_provider === 'vertex-ai' && (
        <>
          <FieldGroup label="GCP Project ID" required>
            <input
              type="text"
              style={inputStyle}
              value={agents.project}
              onChange={e => onUpdate('project', e.target.value)}
              placeholder="my-gcp-project"
            />
          </FieldGroup>
          <FieldGroup label="GCP Region" required>
            <input
              type="text"
              style={inputStyle}
              value={agents.location}
              onChange={e => onUpdate('location', e.target.value)}
              placeholder="us-central1"
            />
          </FieldGroup>
          <FieldGroup label="Credentials File" description="Optional: Path to service account JSON file.">
            <PathPicker
              value={agents.credentials_file}
              onChange={v => onUpdate('credentials_file', v)}
              onBrowse={async () => {
                const f = await onBrowseFile('Select credentials JSON', '*.json');
                if (f) onUpdate('credentials_file', f);
              }}
              placeholder="/path/to/service-account.json"
            />
          </FieldGroup>
        </>
      )}

      {agents.llm_provider === 'ollama' && (
        <>
          <FieldGroup label="Model" description="The Ollama model to use for AI agents.">
            <input
              type="text"
              style={inputStyle}
              value={agents.model}
              onChange={e => onUpdate('model', e.target.value)}
              placeholder="llama3.2"
            />
          </FieldGroup>
          <FieldGroup label="Base URL" description="Ollama server URL.">
            <div style={rowStyle}>
              <input
                type="text"
                style={{ ...inputStyle, flex: 1 }}
                value={agents.base_url}
                onChange={e => onUpdate('base_url', e.target.value)}
                placeholder="http://localhost:11434"
              />
              <StatusDot status={ollamaTest ? (ollamaTest.available ? 'ok' : 'error') : 'unknown'} />
              <button type="button" style={testBtnStyle} onClick={() => onTestOllama(agents.base_url)}>
                Test
              </button>
            </div>
          </FieldGroup>
          {ollamaTest && (
            <div style={{ ...noteStyle, color: ollamaTest.available ? '#a6e3a1' : '#f38ba8' }}>
              {ollamaTest.message}
            </div>
          )}
        </>
      )}

      {/* Advanced AI Options */}
      <SectionHeader title="Advanced AI" defaultExpanded={false}>
        <label style={toggleStyle}>
          <input
            type="checkbox"
            checked={agents.auto_link}
            onChange={e => onUpdate('auto_link', e.target.checked)}
          />
          Auto-Link: Use AI to detect cross-service dependencies
        </label>
        <label style={toggleStyle}>
          <input
            type="checkbox"
            checked={agents.auto_summarize}
            onChange={e => onUpdate('auto_summarize', e.target.checked)}
          />
          Auto-Summarize: Generate AI summaries after indexing
        </label>
      </SectionHeader>

      {/* Embedding */}
      <SectionHeader title="Embedding" description="Vector search for semantic code search." defaultExpanded={false}>
        <FieldGroup label="Embedding Provider" description="Leave empty for auto-detection.">
          <select
            style={selectStyle}
            value={agents.embedding_provider}
            onChange={e => onUpdate('embedding_provider', e.target.value)}
          >
            <option value="">Auto-detect</option>
            <option value="ollama">Ollama</option>
            <option value="vertex-ai">Vertex AI</option>
          </select>
        </FieldGroup>
        <FieldGroup label="Embedding Model" description="Leave empty to use provider default.">
          <input
            type="text"
            style={inputStyle}
            value={agents.embedding_model}
            onChange={e => onUpdate('embedding_model', e.target.value)}
            placeholder="(auto)"
          />
        </FieldGroup>
      </SectionHeader>
    </div>
  );
}
