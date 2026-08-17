package config

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultCatalogAndRows(t *testing.T) {
	value := Default()
	if len(value.Providers) != 4 || len(value.Models) != 13 {
		t.Fatalf("默认目录异常：providers=%d models=%d", len(value.Providers), len(value.Models))
	}
	rows := BuildRows(value)
	if len(rows) != 3 || !strings.HasPrefix(rows[0].Value, "claude-router/opencode-free/") {
		t.Fatalf("未配置 Provider 的模型仍在 rows 中：%+v", rows)
	}
	configured := value.Providers["sub2api"]
	configured.BaseURL = "https://provider.example.com"
	configured.APIKey = "real-key"
	value.Providers["sub2api"] = configured
	rows = BuildRows(value)
	if len(rows) != 6 || rows[0].Context == nil || *rows[0].Context != 1_050_000 || rows[0].Fast == nil || !*rows[0].Fast {
		t.Fatalf("已配置 Provider 模型 rows 异常：%+v", rows)
	}
	disabled := false
	value.Models[0].Enabled = &disabled
	if len(BuildRows(value)) != 5 {
		t.Fatal("停用模型仍出现在 picker")
	}
	if _, ok := RouteForModel(value, value.Models[0].ID); ok {
		t.Fatal("停用模型仍可路由")
	}
	unconfigured := value.Models[3]
	if _, ok := RouteForModel(value, unconfigured.ID); ok {
		t.Fatal("未配置 Provider 的模型仍可路由")
	}
}

func TestStrictParseAndSecrets(t *testing.T) {
	value := Default()
	bytes, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(bytes)
	if err != nil {
		t.Fatal(err)
	}
	public := WithoutSecrets(parsed)
	if strings.Contains(string(mustJSON(t, public)), "REPLACE_WITH") {
		t.Fatal("公开配置泄露 API key")
	}
	if public.Providers["sub2api"].Configured || public.Providers["sub2api"].HasAPIKey || !public.Providers["opencode-free"].Configured {
		t.Fatalf("Provider 配置状态错误：%+v", public.Providers)
	}
	if _, err := Parse(append(bytes[:len(bytes)-1], []byte(`,"extra":true}`)...)); err == nil {
		t.Fatal("未知字段未被拒绝")
	}
	if _, err := Parse(append(bytes, []byte(` {}`)...)); err == nil {
		t.Fatal("尾随 JSON 未被拒绝")
	}
}

func TestProviderConfigurationRequiresURLAndKeyExceptOpenCodeFree(t *testing.T) {
	provider := Provider{BaseURL: "https://provider.example.com", Auth: AuthBearer}
	if ProviderConfigured("demo", provider) {
		t.Fatal("无 key Provider 被判定为已配置")
	}
	provider.APIKey = "secret"
	if !ProviderConfigured("demo", provider) {
		t.Fatal("有效 URL 和 key 未判定为已配置")
	}
	provider.APIKey = "REPLACE_WITH_KEY"
	if ProviderConfigured("demo", provider) {
		t.Fatal("占位 key 被判定为已配置")
	}
	provider = Provider{BaseURL: "https://opencode.ai/zen/v1", Auth: AuthNone}
	if !ProviderConfigured("opencode-free", provider) || ProviderConfigured("other", provider) {
		t.Fatal("仅 opencode-free 应允许免 key")
	}
}

func TestProviderBaseURLMustBeAbsoluteWithoutQuery(t *testing.T) {
	for _, baseURL := range []string{"https:///missing-host", "https://provider.invalid/v1?token=secret"} {
		value := Default()
		provider := value.Providers["deepseek"]
		provider.BaseURL = baseURL
		value.Providers["deepseek"] = provider
		if err := Validate(value); err == nil {
			t.Fatalf("非法 Provider baseUrl 未被拒绝：%s", baseURL)
		}
	}
}

func TestSaveAndLoad(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "claude-patch.exe")
	value := Default()
	if err := Save(PathBeside(executable), value); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(executable)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Exists || len(loaded.Config.Models) != len(value.Models) {
		t.Fatalf("配置读取异常：%+v", loaded)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	bytes, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return bytes
}
