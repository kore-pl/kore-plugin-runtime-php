package phpop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// jsonRaw 是测试里构造 require 键值用的原始 JSON 简写。
type jsonRaw = json.RawMessage

// writeFile 建父目录并写入文件，失败直接终止用例。
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
}

// TestProjectsScan 真实扫描临时目录：正常项目 + require.php + 框架识别 + 坏 JSON + 跳过 vendor/隐藏/超深。
func TestProjectsScan(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "good-app", "composer.json"), `{
  "name": "acme/good-app",
  "require": {
    "php": "^8.1",
    "ext-mbstring": "*",
    "laravel/framework": "^10.0",
    "monolog/monolog": "^3.0"
  }
}`)
	writeFile(t, filepath.Join(root, "good-app", "composer.lock"), `{}`)
	writeFile(t, filepath.Join(root, "bad-json", "composer.json"), `{ "name": "broken", `)
	writeFile(t, filepath.Join(root, "no-composer", "index.php"), `<?php echo "hi";`)
	// vendor 里是第三方包的 composer.json，绝不能被当成本项目。
	writeFile(t, filepath.Join(root, "good-app", "vendor", "league", "composer.json"), `{"name":"league/x"}`)
	// 深度 3 的项目应被收录，深度 4 的不应被扫描到。
	writeFile(t, filepath.Join(root, "deep", "a", "b", "composer.json"), `{"name":"acme/deep"}`)
	writeFile(t, filepath.Join(root, "deep", "a", "b", "c", "composer.json"), `{"name":"acme/too-deep"}`)
	// 无 name 时回退到目录名。
	writeFile(t, filepath.Join(root, "unnamed", "composer.json"), `{"require":{"symfony/framework-bundle":"^6.0"}}`)
	// 隐藏目录不扫描。
	writeFile(t, filepath.Join(root, ".hidden", "composer.json"), `{"name":"acme/hidden"}`)

	runtime := &Runtime{}
	result, err := runtime.Projects(root)
	if err != nil {
		t.Fatalf("Projects 失败: %v", err)
	}
	if result.Count != 3 {
		t.Fatalf("期望 3 个项目，实际 %d：%+v", result.Count, result.Projects)
	}
	if result.Count != len(result.Projects) {
		t.Fatalf("count=%d 与 projects 长度 %d 不一致", result.Count, len(result.Projects))
	}
	byName := map[string]Project{}
	for _, project := range result.Projects {
		byName[project.Name] = project
		if strings.Contains(project.Path, "vendor") {
			t.Fatalf("vendor 内的 composer.json 不应被收录: %+v", project)
		}
		if strings.Contains(project.Path, "too-deep") || strings.Contains(project.Path, ".hidden") {
			t.Fatalf("超深/隐藏目录不应被收录: %+v", project)
		}
	}
	good, ok := byName["acme/good-app"]
	if !ok {
		t.Fatalf("缺少 good-app：%+v", result.Projects)
	}
	if good.PHPRequirement != "^8.1" {
		t.Fatalf("php_requirement=%q，期望 ^8.1", good.PHPRequirement)
	}
	// 只统计带 vendor 前缀的真实包：laravel/framework、monolog/monolog；ext-mbstring 与 php 不算。
	if good.DependenciesCount != 2 {
		t.Fatalf("dependencies_count=%d，期望 2", good.DependenciesCount)
	}
	if !good.HasLock {
		t.Fatal("good-app 应有 composer.lock")
	}
	if good.Framework != "laravel" {
		t.Fatalf("framework=%q，期望 laravel", good.Framework)
	}
	deep, ok := byName["acme/deep"]
	if !ok || deep.Framework != "" || deep.HasLock {
		t.Fatalf("深度 3 项目字段异常: %+v", deep)
	}
	unnamed, ok := byName["unnamed"]
	if !ok {
		t.Fatalf("缺少 name 时应回退目录名：%+v", result.Projects)
	}
	if unnamed.Framework != "symfony" {
		t.Fatalf("framework=%q，期望 symfony", unnamed.Framework)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("坏 JSON 应产生 warnings")
	}
	joined := strings.Join(result.Warnings, " | ")
	if !strings.Contains(joined, "bad-json") {
		t.Fatalf("warnings 未指出坏 JSON 路径: %v", result.Warnings)
	}
	if strings.Count(joined, "解析失败") != 1 {
		t.Fatalf("坏 JSON 应恰好一条告警: %v", result.Warnings)
	}
}

// TestProjectsEmpty 空目录返回空数组语义（nil 由 callProjects 归一）。
func TestProjectsEmpty(t *testing.T) {
	root := t.TempDir()
	runtime := &Runtime{}
	result, err := runtime.Projects(root)
	if err != nil {
		t.Fatalf("Projects 失败: %v", err)
	}
	if result.Count != 0 || len(result.Projects) != 0 {
		t.Fatalf("空目录应返回 0 个项目: %+v", result)
	}
}

// TestCountPackages 平台需求不计入依赖数。
func TestCountPackages(t *testing.T) {
	require := map[string]jsonRaw{
		"php":               jsonRaw(`"^8.1"`),
		"ext-json":          jsonRaw(`"*"`),
		"lib-curl":          jsonRaw(`"*"`),
		"composer-runtime":  jsonRaw(`">=2.0"`),
		"laravel/framework": jsonRaw(`"^10.0"`),
	}
	if got := countPackages(require); got != 1 {
		t.Fatalf("countPackages=%d，期望 1", got)
	}
}

// TestDetectFramework 三种框架与识别不出时的空串。
func TestDetectFramework(t *testing.T) {
	cases := []struct {
		require map[string]jsonRaw
		want    string
	}{
		{map[string]jsonRaw{"laravel/framework": jsonRaw(`"^10"`)}, "laravel"},
		{map[string]jsonRaw{"symfony/symfony": jsonRaw(`"^6"`)}, "symfony"},
		{map[string]jsonRaw{"topthink/think": jsonRaw(`"^8"`)}, "thinkphp"},
		{map[string]jsonRaw{"monolog/monolog": jsonRaw(`"^3"`)}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		if got := detectFramework(c.require); got != c.want {
			t.Fatalf("detectFramework(%v)=%q，期望 %q", c.require, got, c.want)
		}
	}
}
