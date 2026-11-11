package docs

import "github.com/imyousuf/CodeEagle/pkg/kit/topics"

// TopicExtractionPrompt is the system prompt for extracting topics from text content.
const TopicExtractionPrompt = topics.TopicExtractionPrompt

// ImageDescriptionPrompt is the system prompt for describing image content.
const ImageDescriptionPrompt = topics.ImageDescriptionPrompt

// NoThinkSuffix is appended to prompts when thinking mode is disabled.
const NoThinkSuffix = " /no_think"
