package docs

// TopicExtractionPrompt is the system prompt for extracting topics from text content.
const TopicExtractionPrompt = `Extract topics, entities, and key concepts from the following text. If the text is very long, first summarize it then extract. Output JSON with: {"summary": "2-3 sentence summary", "topics": ["topic1", "topic2", ...], "entities": ["FunctionName", "ClassName", "package_name", ...]}. Be concise. Topics should be abstract themes, not specific names.`

// ImageDescriptionPrompt is the system prompt for describing image content.
const ImageDescriptionPrompt = `Describe this image in detail. If it is a diagram, describe the components, flow, and relationships. Extract any text visible in the image. Output JSON with: {"summary": "2-3 sentence description", "topics": ["topic1", "topic2", ...], "entities": ["visible names, labels, identifiers", ...]}.`

// NoThinkSuffix is appended to prompts when thinking mode is disabled.
const NoThinkSuffix = " /no_think"
