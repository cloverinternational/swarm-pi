package ollama

// CloudModels are models available on Ollama Cloud (ollama.com)
var CloudModels = []ModelDefinition{
	{
		ID:            "gpt-oss:120b",
		Name:          "GPT-OSS 120B",
		ContextWindow: 128000,
		Description:   "Large GPT-OSS model with 120B parameters",
	},
	{
		ID:            "gpt-oss:20b",
		Name:          "GPT-OSS 20B",
		ContextWindow: 128000,
		Description:   "GPT-OSS model with 20B parameters",
	},
	{
		ID:            "glm-4",
		Name:          "GLM-4",
		ContextWindow: 128000,
		Description:   "GLM-4 model from Zhipu AI",
	},
	{
		ID:            "glm-5",
		Name:          "GLM-5",
		ContextWindow: 128000,
		Description:   "GLM-5 model from Zhipu AI",
	},
	{
		ID:            "deepseek-r1",
		Name:          "DeepSeek R1",
		ContextWindow: 128000,
		Description:   "DeepSeek R1 reasoning model",
	},
	{
		ID:            "qwen3-coder",
		Name:          "Qwen3 Coder",
		ContextWindow: 128000,
		Description:   "Qwen3 coding assistant model",
	},
}

// CommonLocalModels are popular models users can pull locally
var CommonLocalModels = []ModelDefinition{
	{
		ID:            "llama3.2",
		Name:          "Llama 3.2",
		ContextWindow: 128000,
		Description:   "Meta's Llama 3.2 model",
	},
	{
		ID:            "llama3.1",
		Name:          "Llama 3.1",
		ContextWindow: 128000,
		Description:   "Meta's Llama 3.1 model",
	},
	{
		ID:            "mistral",
		Name:          "Mistral",
		ContextWindow: 32000,
		Description:   "Mistral AI's base model",
	},
	{
		ID:            "codellama",
		Name:          "Code Llama",
		ContextWindow: 16384,
		Description:   "Meta's Code Llama for programming",
	},
	{
		ID:            "qwen2.5",
		Name:          "Qwen 2.5",
		ContextWindow: 128000,
		Description:   "Alibaba's Qwen 2.5 model",
	},
	{
		ID:            "deepseek-r1",
		Name:          "DeepSeek R1",
		ContextWindow: 128000,
		Description:   "DeepSeek R1 reasoning model",
	},
	{
		ID:            "glm-4",
		Name:          "GLM-4",
		ContextWindow: 128000,
		Description:   "Zhipu AI GLM-4 model",
	},
	{
		ID:            "glm-5",
		Name:          "GLM-5",
		ContextWindow: 128000,
		Description:   "Zhipu AI GLM-5 model",
	},
}

// ModelDefinition represents a model's metadata
type ModelDefinition struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContextWindow int    `json:"contextWindow"`
	Description   string `json:"description,omitempty"`
}

// GetModelByID finds a model by ID in the given list
func GetModelByID(models []ModelDefinition, id string) *ModelDefinition {
	for i := range models {
		if models[i].ID == id {
			return &models[i]
		}
	}
	return nil
}

// GetAllModels returns both cloud and local models
func GetAllModels() []ModelDefinition {
	result := make([]ModelDefinition, len(CloudModels))
	copy(result, CloudModels)
	result = append(result, CommonLocalModels...)
	return result
}
