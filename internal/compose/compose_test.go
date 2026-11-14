package compose

import (
	"reflect"
	"testing"
)

func TestPortMappingString(t *testing.T) {
	tests := []struct {
		in   PortMapping
		want string
	}{
		{PortMapping{Target: 80, Published: "8080", Protocol: "tcp"}, "8080:80/tcp"},
		{PortMapping{Target: 53, Published: "5353", Protocol: "udp"}, "5353:53/udp"},
		{PortMapping{Target: 443, Protocol: ""}, "443/tcp"},
		{PortMapping{Target: 443, Published: "none"}, "443/tcp"},
	}
	for _, tc := range tests {
		if got := tc.in.String(); got != tc.want {
			t.Errorf("%+v.String() = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestVolumeMappingString(t *testing.T) {
	tests := []struct {
		in   VolumeMapping
		want string
	}{
		{VolumeMapping{Source: "data", Target: "/var/lib"}, "data:/var/lib"},
		{VolumeMapping{Source: "./src", Target: "/app", ReadOnly: true}, "./src:/app:ro"},
		{VolumeMapping{Target: "/anon"}, "anonymous:/anon"},
	}
	for _, tc := range tests {
		if got := tc.in.String(); got != tc.want {
			t.Errorf("%+v.String() = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestProjectServiceNames(t *testing.T) {
	p := Project{Config: ProjectConfig{Services: map[string]ServiceConfig{
		"web":   {},
		"db":    {},
		"cache": {},
	}}}
	got := p.ServiceNames()
	want := []string{"cache", "db", "web"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ServiceNames() = %v, want %v", got, want)
	}
}

func TestProjectServiceNamesEmpty(t *testing.T) {
	p := Project{}
	if got := p.ServiceNames(); len(got) != 0 {
		t.Errorf("空项目应返回空列表, got %v", got)
	}
}

func TestFindComposeFile(t *testing.T) {
	dir := t.TempDir()
	if got := FindComposeFile(dir); got != "" {
		t.Errorf("空目录应返回空字符串, got %q", got)
	}
	if IsComposeDir(dir) {
		t.Error("空目录不应被视为 compose 目录")
	}

	// 优先级：compose.yaml 高于 docker-compose.yml
	writeFile(t, dir, "docker-compose.yml", "services: {}\n")
	if got := FindComposeFile(dir); got != filepathJoin(dir, "docker-compose.yml") {
		t.Errorf("应找到 docker-compose.yml, got %q", got)
	}
	writeFile(t, dir, "compose.yaml", "services: {}\n")
	if got := FindComposeFile(dir); got != filepathJoin(dir, "compose.yaml") {
		t.Errorf("compose.yaml 应优先, got %q", got)
	}
	if !IsComposeDir(dir) {
		t.Error("含 compose.yaml 的目录应被视为 compose 目录")
	}
}

func TestScan(t *testing.T) {
	root := t.TempDir()

	// a: 直接含 compose.yaml
	dirA := filepathJoin(root, "a")
	mkdirAll(t, dirA)
	writeFile(t, dirA, "compose.yaml", "services: {}\n")

	// b/nested: 两层深
	dirB := filepathJoin(root, "b", "nested")
	mkdirAll(t, dirB)
	writeFile(t, dirB, "docker-compose.yml", "services: {}\n")

	// node_modules 里的应被跳过
	dirNoise := filepathJoin(root, "node_modules", "pkg")
	mkdirAll(t, dirNoise)
	writeFile(t, dirNoise, "compose.yaml", "services: {}\n")

	// c: 无 compose 文件
	mkdirAll(t, filepathJoin(root, "c"))

	got := Scan(ScanOptions{Roots: []string{root}, Depth: 3})
	dirs := make([]string, 0, len(got))
	for _, f := range got {
		dirs = append(dirs, f.Dir)
	}
	if len(dirs) != 2 {
		t.Fatalf("应找到 2 个项目（跳过 node_modules）, got %v", dirs)
	}
	found := map[string]bool{}
	for _, d := range dirs {
		found[d] = true
	}
	if !found[dirA] || !found[dirB] {
		t.Errorf("缺少预期项目: %v", dirs)
	}
}

func TestScanDepthLimit(t *testing.T) {
	root := t.TempDir()
	deep := filepathJoin(root, "x", "y", "z")
	mkdirAll(t, deep)
	writeFile(t, deep, "compose.yaml", "services: {}\n")

	if got := Scan(ScanOptions{Roots: []string{root}, Depth: 1}); len(got) != 0 {
		t.Errorf("深度 1 不应找到深度 3 的项目, got %v", got)
	}
	if got := Scan(ScanOptions{Roots: []string{root}, Depth: 5}); len(got) != 1 {
		t.Errorf("深度 5 应找到项目, got %v", got)
	}
}

func TestScanStopsAtProject(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "compose.yaml", "services: {}\n")
	// 子目录里的不应单独算一个项目（父项目已经命中）
	sub := filepathJoin(root, "examples")
	mkdirAll(t, sub)
	writeFile(t, sub, "compose.yaml", "services: {}\n")

	got := Scan(ScanOptions{Roots: []string{root}, Depth: 4})
	if len(got) != 1 {
		t.Errorf("命中父项目后不应继续深入, got %v", got)
	}
}

func TestScanMissingRoot(t *testing.T) {
	got := Scan(ScanOptions{Roots: []string{`Z:\definitely\not\here`}, Depth: 2})
	if len(got) != 0 {
		t.Errorf("不存在的根目录应返回空, got %v", got)
	}
}
