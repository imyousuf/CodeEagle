import { useState, useEffect, useCallback } from 'react';
import type { AgentInfo, ChatMessage } from '../types';

export function useAgent() {
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [selectedAgent, setSelectedAgent] = useState('planner');
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Load agent types on mount.
  useEffect(() => {
    if (window.go?.app?.App?.GetAgentTypes) {
      window.go.app.App.GetAgentTypes()
        .then((types) => {
          setAgents(types);
          if (types.length > 0) {
            setSelectedAgent(types[0].id);
          }
        })
        .catch(console.error);
    }
  }, []);

  // Listen for Wails events.
  useEffect(() => {
    if (!window.runtime?.EventsOn) return;

    const offThinking = window.runtime.EventsOn('agent:thinking', () => {
      setLoading(true);
      setError(null);
    });

    const offResponse = window.runtime.EventsOn('agent:response', (data: unknown) => {
      const d = data as { agent: string; content: string };
      setMessages((prev) => [
        ...prev,
        { role: 'assistant', content: d.content, agent: d.agent },
      ]);
      setLoading(false);
    });

    const offError = window.runtime.EventsOn('agent:error', (data: unknown) => {
      const d = data as { agent: string; error: string };
      setError(d.error);
      setLoading(false);
    });

    return () => {
      offThinking();
      offResponse();
      offError();
    };
  }, []);

  const sendMessage = useCallback(async (query: string) => {
    if (!window.go?.app?.App?.AskAgent) {
      setError('Agent backend not available');
      return;
    }

    // Add user message.
    setMessages((prev) => [
      ...prev,
      { role: 'user', content: query, agent: selectedAgent },
    ]);

    try {
      await window.go.app.App.AskAgent(selectedAgent, query);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }, [selectedAgent]);

  const clearMessages = useCallback(() => {
    setMessages([]);
    setError(null);
  }, []);

  return {
    agents,
    selectedAgent,
    setSelectedAgent,
    messages,
    loading,
    error,
    sendMessage,
    clearMessages,
  };
}
