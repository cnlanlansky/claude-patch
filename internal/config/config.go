package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Protocol string

const (
	AnthropicMessages Protocol = "anthropic-messages"
	OpenAIResponses   Protocol = "openai-responses"
	OpenAIChat        Protocol = "openai-chat-completions"
)

type AuthMode string

const (
	AuthAPIKey AuthMode = "x-api-key"
	AuthBearer AuthMode = "bearer"
	AuthNone   AuthMode = "none"
)

type Claude struct {
	Executable       string `json:"executable"`
	WorkingDirectory string `json:"workingDirectory,omitempty"`
}

type Provider struct {
	Label    string   `json:"label"`
	BaseURL  string   `json:"baseUrl"`
	Protocol Protocol `json:"protocol"`
	Auth     AuthMode `json:"auth"`
	APIKey   string   `json:"apiKey,omitempty"`
}

type Model struct {
	ID            string    `json:"id"`
	Label         string    `json:"label"`
	Provider      string    `json:"provider"`
	UpstreamModel string    `json:"upstreamModel"`
	Protocol      *Protocol `json:"protocol,omitempty"`
	ContextWindow *int      `json:"contextWindow,omitempty"`
	Fast          *bool     `json:"fast,omitempty"`
	Enabled       *bool     `json:"enabled,omitempty"`
}

type Config struct {
	Claude    Claude              `json:"claude"`
	Providers map[string]Provider `json:"providers"`
	Models    []Model             `json:"models"`
}

type Loaded struct {
	Path   string
	Exists bool
	Config Config
}

type PickerRow struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Context     *int   `json:"context,omitempty"`
	Fast        *bool  `json:"fast,omitempty"`
}

type Route struct {
	Model    Model
	Provider Provider
	Protocol Protocol
}

type PublicProvider struct {
	Label      string   `json:"label"`
	BaseURL    string   `json:"baseUrl"`
	Protocol   Protocol `json:"protocol"`
	Auth       AuthMode `json:"auth"`
	HasAPIKey  bool     `json:"hasApiKey"`
	Configured bool     `json:"configured"`
}

type PublicConfig struct {
	Claude    Claude                    `json:"claude"`
	Providers map[string]PublicProvider `json:"providers"`
	Models    []Model                   `json:"models"`
}

var (
	providerIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	modelIDPattern    = regexp.MustCompile(`^claude-router/[a-z0-9][a-z0-9-]*/.+$`)
)

func boolPtr(value bool) *bool             { return &value }
func intPtr(value int) *int                { return &value }
func protocolPtr(value Protocol) *Protocol { return &value }

func model(provider, upstream, label string, protocol *Protocol, context int, fast *bool) Model {
	return Model{
		ID:            "claude-router/" + provider + "/" + upstream,
		Label:         label,
		Provider:      provider,
		UpstreamModel: upstream,
		Protocol:      protocol,
		ContextWindow: intPtr(context),
		Fast:          fast,
		Enabled:       boolPtr(true),
	}
}

func Default() Config {
	return Config{
		Claude: Claude{},
		Providers: map[string]Provider{
			"sub2api":       {Label: "Sub2API", BaseURL: "https://sub2api.example.invalid", Protocol: AnthropicMessages, Auth: AuthBearer, APIKey: "REPLACE_WITH_SUB2API_API_KEY"},
			"deepseek":      {Label: "DeepSeek", BaseURL: "https://api.deepseek.com/anthropic", Protocol: AnthropicMessages, Auth: AuthAPIKey, APIKey: "REPLACE_WITH_DEEPSEEK_API_KEY"},
			"opencode-free": {Label: "OpenCode Free", BaseURL: "https://opencode.ai/zen/v1", Protocol: OpenAIChat, Auth: AuthNone},
			"opencode-go":   {Label: "OpenCode Go", BaseURL: "https://opencode.ai/zen/go/v1", Protocol: OpenAIResponses, Auth: AuthBearer, APIKey: "REPLACE_WITH_OPENCODE_GO_API_KEY"},
		},
		Models: []Model{
			model("sub2api", "gpt-5.6-sol", "Sub2API / GPT 5.6 Sol", nil, 1050000, boolPtr(true)),
			model("sub2api", "gpt-5.6-luna", "Sub2API / GPT 5.6 Luna", nil, 1050000, boolPtr(true)),
			model("sub2api", "gpt-5.6-terra", "Sub2API / GPT 5.6 Terra", nil, 1050000, boolPtr(true)),
			model("deepseek", "deepseek-v4-pro", "DeepSeek / V4 Pro", nil, 1048576, nil),
			model("deepseek", "deepseek-v4-flash", "DeepSeek / V4 Flash", nil, 1048576, nil),
			model("opencode-free", "deepseek-v4-flash-free", "OpenCode Free / DeepSeek V4 Flash Free", protocolPtr(OpenAIChat), 200000, nil),
			model("opencode-free", "big-pickle", "OpenCode Free / Big Pickle", protocolPtr(OpenAIChat), 200000, nil),
			model("opencode-free", "mimo-v2.5-free", "OpenCode Free / MiMo V2.5 Free", protocolPtr(OpenAIChat), 200000, nil),
			model("opencode-go", "deepseek-v4-flash", "OpenCode Go / DeepSeek V4 Flash", protocolPtr(OpenAIChat), 1000000, nil),
			model("opencode-go", "mimo-v2.5", "OpenCode Go / MiMo V2.5", protocolPtr(OpenAIChat), 1000000, nil),
			model("opencode-go", "hy3", "OpenCode Go / Hy3", protocolPtr(OpenAIChat), 256000, nil),
			model("opencode-go", "deepseek-v4-pro", "OpenCode Go / DeepSeek V4 Pro", protocolPtr(OpenAIChat), 1000000, nil),
			model("opencode-go", "minimax-m3", "OpenCode Go / MiniMax M3", protocolPtr(AnthropicMessages), 1000000, nil),
		},
	}
}

func enabled(model Model) bool { return model.Enabled == nil || *model.Enabled }

func BuildRows(value Config) []PickerRow {
	rows := make([]PickerRow, 0, len(value.Models))
	for _, model := range value.Models {
		if !enabled(model) {
			continue
		}
		provider, exists := value.Providers[model.Provider]
		if !exists || !ProviderConfigured(model.Provider, provider) {
			continue
		}
		rows = append(rows, PickerRow{
			Value: model.ID, Label: model.Label,
			Description: "通过 " + provider.Label,
			Context:     model.ContextWindow, Fast: model.Fast,
		})
	}
	return rows
}

func RouteForModel(value Config, id string) (Route, bool) {
	for _, model := range value.Models {
		if model.ID != id || !enabled(model) {
			continue
		}
		provider, ok := value.Providers[model.Provider]
		if !ok || !ProviderConfigured(model.Provider, provider) {
			return Route{}, false
		}
		protocol := provider.Protocol
		if model.Protocol != nil {
			protocol = *model.Protocol
		}
		return Route{Model: model, Provider: provider, Protocol: protocol}, true
	}
	return Route{}, false
}

func ProviderConfigured(id string, provider Provider) bool {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(provider.BaseURL))
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" || strings.HasSuffix(strings.ToLower(parsed.Hostname()), ".invalid") {
		return false
	}
	if id == "opencode-free" && provider.Auth == AuthNone {
		return true
	}
	key := strings.TrimSpace(provider.APIKey)
	return key != "" && !strings.HasPrefix(key, "REPLACE_WITH_")
}

func WithoutSecrets(value Config) PublicConfig {
	providers := make(map[string]PublicProvider, len(value.Providers))
	for id, provider := range value.Providers {
		hasAPIKey := strings.TrimSpace(provider.APIKey) != "" && !strings.HasPrefix(strings.TrimSpace(provider.APIKey), "REPLACE_WITH_")
		providers[id] = PublicProvider{
			Label: provider.Label, BaseURL: provider.BaseURL, Protocol: provider.Protocol,
			Auth: provider.Auth, HasAPIKey: hasAPIKey, Configured: ProviderConfigured(id, provider),
		}
	}
	models := append([]Model(nil), value.Models...)
	return PublicConfig{Claude: value.Claude, Providers: providers, Models: models}
}

func PathBeside(executable string) string {
	return filepath.Join(filepath.Dir(executable), "config.json")
}

func Load(executable string) (Loaded, error) {
	path := PathBeside(executable)
	bytes, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Loaded{Path: path, Config: Default()}, nil
	}
	if err != nil {
		return Loaded{}, err
	}
	value, err := Parse(bytes)
	if err != nil {
		return Loaded{}, err
	}
	applyDefaultProfiles(&value)
	return Loaded{Path: path, Exists: true, Config: value}, nil
}

func Parse(bytes []byte) (Config, error) {
	var value Config
	decoder := json.NewDecoder(strings.NewReader(string(bytes)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return Config{}, fmt.Errorf("config.json: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Config{}, errors.New("config.json: 只能包含一个 JSON 对象")
		}
		return Config{}, fmt.Errorf("config.json: %w", err)
	}
	if err := Validate(value); err != nil {
		return Config{}, err
	}
	return value, nil
}

func Validate(value Config) error {
	if len(value.Claude.Executable) > 2048 || len(value.Claude.WorkingDirectory) > 2048 {
		return errors.New("config.json.claude: 路径长度不能超过 2048")
	}
	for rawID, provider := range value.Providers {
		if rawID != strings.ToLower(rawID) || !providerIDPattern.MatchString(rawID) || len(rawID) > 64 {
			return fmt.Errorf("config.json.providers.%s: Provider ID 非法", rawID)
		}
		if strings.TrimSpace(provider.Label) == "" || len(strings.TrimSpace(provider.Label)) > 200 {
			return fmt.Errorf("config.json.providers.%s.label: 必须是 1-200 字符", rawID)
		}
		parsed, err := url.ParseRequestURI(provider.BaseURL)
		if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" {
			return fmt.Errorf("config.json.providers.%s.baseUrl: 必须是无凭据、query 和 fragment 的绝对 HTTP(S) URL", rawID)
		}
		if !validProtocol(provider.Protocol) {
			return fmt.Errorf("config.json.providers.%s.protocol: 不支持的协议", rawID)
		}
		if provider.Auth != AuthAPIKey && provider.Auth != AuthBearer && provider.Auth != AuthNone {
			return fmt.Errorf("config.json.providers.%s.auth: 不支持的认证方式", rawID)
		}
		if len(strings.TrimSpace(provider.APIKey)) > 8192 {
			return fmt.Errorf("config.json.providers.%s.apiKey: 长度不能超过 8192", rawID)
		}
	}
	ids := make(map[string]struct{}, len(value.Models))
	for index, model := range value.Models {
		path := fmt.Sprintf("config.json.models[%d]", index)
		if !modelIDPattern.MatchString(model.ID) || len(model.ID) > 512 {
			return fmt.Errorf("%s.id: 必须符合 claude-router/<provider>/<model>", path)
		}
		if _, exists := ids[model.ID]; exists {
			return fmt.Errorf("%s.id: 模型 ID 重复", path)
		}
		ids[model.ID] = struct{}{}
		if !providerIDPattern.MatchString(model.Provider) || !strings.HasPrefix(model.ID, "claude-router/"+model.Provider+"/") {
			return fmt.Errorf("%s.provider: 与模型 ID 不一致", path)
		}
		if _, exists := value.Providers[model.Provider]; !exists {
			return fmt.Errorf("%s.provider: 引用的 Provider 不存在", path)
		}
		if strings.TrimSpace(model.Label) == "" || len(strings.TrimSpace(model.Label)) > 200 {
			return fmt.Errorf("%s.label: 必须是 1-200 字符", path)
		}
		if strings.TrimSpace(model.UpstreamModel) == "" || len(strings.TrimSpace(model.UpstreamModel)) > 512 {
			return fmt.Errorf("%s.upstreamModel: 必须是 1-512 字符", path)
		}
		if model.Protocol != nil && !validProtocol(*model.Protocol) {
			return fmt.Errorf("%s.protocol: 不支持的协议", path)
		}
		if model.ContextWindow != nil && (*model.ContextWindow <= 0 || *model.ContextWindow > 2_000_000) {
			return fmt.Errorf("%s.contextWindow: 必须是 1-2000000", path)
		}
	}
	return nil
}

func validProtocol(value Protocol) bool {
	return value == AnthropicMessages || value == OpenAIResponses || value == OpenAIChat
}

func Save(path string, value Config) error {
	if err := Validate(value); err != nil {
		return err
	}
	bytes, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return err
	}
	temporary := fmt.Sprintf("%s.%d.%s.tmp", path, os.Getpid(), hex.EncodeToString(random))
	defer os.Remove(temporary)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(temporary, append(bytes, '\n'), 0o600); err != nil {
		return err
	}
	return replaceFile(temporary, path)
}

func applyDefaultProfiles(value *Config) {
	profiles := make(map[string]Model)
	for _, model := range Default().Models {
		profiles[model.ID] = model
	}
	for index := range value.Models {
		defaults, ok := profiles[value.Models[index].ID]
		if !ok {
			continue
		}
		if value.Models[index].ContextWindow == nil {
			value.Models[index].ContextWindow = defaults.ContextWindow
		}
		if value.Models[index].Fast == nil {
			value.Models[index].Fast = defaults.Fast
		}
		if value.Models[index].Enabled == nil {
			value.Models[index].Enabled = defaults.Enabled
		}
	}
}
