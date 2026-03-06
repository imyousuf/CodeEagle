import { useSettings } from '../hooks/useSettings';
import SectionHeader from '../components/settings/SectionHeader';
import ProjectSection from '../components/settings/ProjectSection';
import RepositoriesSection from '../components/settings/RepositoriesSection';
import LanguagesSection from '../components/settings/LanguagesSection';
import AISection from '../components/settings/AISection';
import DocumentsSection from '../components/settings/DocumentsSection';
import AdvancedSection from '../components/settings/AdvancedSection';
import SaveBar from '../components/settings/SaveBar';

const pageStyle: React.CSSProperties = {
  maxWidth: '900px',
  margin: '0 auto',
  paddingBottom: '60px',
};

const headerStyle: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'space-between',
  marginBottom: '20px',
};

const titleStyle: React.CSSProperties = {
  fontSize: '20px',
  fontWeight: 700,
  color: '#cdd6f4',
};

const pathStyle: React.CSSProperties = {
  fontSize: '12px',
  color: '#6c7086',
  fontFamily: 'monospace',
  marginTop: '4px',
};

const detectBtnStyle: React.CSSProperties = {
  padding: '8px 16px',
  fontSize: '13px',
  background: '#89b4fa',
  border: 'none',
  borderRadius: '6px',
  color: '#1e1e2e',
  fontWeight: 600,
  cursor: 'pointer',
};

const loadingStyle: React.CSSProperties = {
  textAlign: 'center',
  padding: '40px',
  color: '#6c7086',
  fontSize: '14px',
};

interface Props {
  onConfigSaved?: () => void;
}

export default function Settings({ onConfigSaved }: Props) {
  const settings = useSettings(onConfigSaved);

  if (!settings.draft) {
    return <div style={loadingStyle}>Loading configuration...</div>;
  }

  const { draft } = settings;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <div style={{ flex: 1, overflow: 'auto' }}>
        <div style={pageStyle}>
          {/* Header */}
          <div style={headerStyle}>
            <div>
              <div style={titleStyle}>Settings</div>
              {settings.configPath && <div style={pathStyle}>{settings.configPath}</div>}
            </div>
            <button
              type="button"
              style={{ ...detectBtnStyle, opacity: settings.detecting ? 0.6 : 1 }}
              onClick={settings.detectAll}
              disabled={settings.detecting}
            >
              {settings.detecting ? 'Detecting...' : 'Auto-Detect All'}
            </button>
          </div>

          {/* Project */}
          <SectionHeader title="Project">
            <ProjectSection
              name={draft.project.name}
              onChange={name => settings.updateDraft('project', 'name', name)}
            />
          </SectionHeader>

          {/* Repositories */}
          <SectionHeader title="Repositories">
            <RepositoriesSection
              repositories={draft.repositories}
              onUpdate={settings.updateRepository}
              onAdd={settings.addRepository}
              onRemove={settings.removeRepository}
              onBrowse={settings.browseDirectory}
              onValidate={settings.validatePath}
            />
          </SectionHeader>

          {/* Languages */}
          <SectionHeader title="Languages">
            <LanguagesSection
              selected={draft.languages}
              allLanguages={settings.allLanguages}
              onChange={langs => settings.updateDraftField('languages', langs)}
              onDetect={() => {
                for (const repo of draft.repositories) {
                  if (repo.path) settings.detectLanguagesForPath(repo.path);
                }
              }}
            />
          </SectionHeader>

          {/* AI */}
          <SectionHeader title="AI Configuration">
            <AISection
              agents={draft.agents}
              detection={settings.detection}
              testResults={settings.testResults}
              onUpdate={(field, value) => settings.updateDraft('agents', field, value)}
              onTestLLM={settings.testLLM}
              onTestOllama={settings.testOllama}
              onBrowseFile={settings.browseFile}
            />
          </SectionHeader>

          {/* Documents */}
          <SectionHeader title="Documents" description="Non-code file indexing (images, PDFs, Office docs)" defaultExpanded={false}>
            <DocumentsSection
              docs={draft.docs}
              onUpdate={(field, value) => settings.updateDraft('docs', field, value)}
              onUpdateFaces={settings.updateFaces}
              onAddExtension={settings.addExcludeExtension}
              onRemoveExtension={settings.removeExcludeExtension}
              onBrowseDir={settings.browseDirectory}
            />
          </SectionHeader>

          {/* Advanced */}
          <SectionHeader title="Advanced" defaultExpanded={false}>
            <AdvancedSection
              excludePatterns={draft.watch.exclude}
              onAddPattern={settings.addExcludePattern}
              onRemovePattern={settings.removeExcludePattern}
              graphStorage="embedded"
            />
          </SectionHeader>
        </div>
      </div>

      {/* Save Bar */}
      <SaveBar
        isDirty={settings.isDirty}
        onSave={settings.saveConfig}
        onReset={settings.resetDraft}
        onPreview={settings.previewChanges}
        saving={settings.saving}
        message={settings.saveMessage}
      />
    </div>
  );
}
