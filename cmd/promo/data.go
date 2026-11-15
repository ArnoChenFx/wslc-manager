package main

// 本文件是宣传片里演示实例显示的数据集。
//
// 全部是虚构的：项目目录、容器名、镜像大小都与这台机器无关，
// 也不会把使用者本机的路径或容器泄露进宣传素材。选的是一眼就懂的常见栈
// （nginx / node / postgres / redis），而不是某个真实业务。
//
// 数据分成「基础」和「随剧情变化的部分」：容器的运行状态、镜像是否已拉下来，
// 都由 world 描述，timeline.go 按帧算出 world，再由这里生成对应的列表。

import (
	"github.com/arno/wslc-manager/internal/compose"
	"github.com/arno/wslc-manager/internal/wslc"
)

// 虚构的项目目录。
const (
	shopDir = `D:\dev\shop`
	blogDir = `D:\dev\blog`
)

// 剧情里要点名的容器与镜像。
const (
	mongoName = "dev-mongo"
	pulledRef = "python:3.13-slim"
)

// world 是剧情在某一帧的「世界状态」：哪些东西已经变了。
//
// 它只描述会变的部分，其余数据是常量。这样每一帧的画面完全由 world 决定，
// 渲染顺序、重复构建都不影响结果。
type world struct {
	// mongoRunning 表示 dev-mongo 已被启动（容器页的「启动」那一幕）。
	mongoRunning bool
	// pulled 表示 python 镜像已经拉取完成，出现在镜像列表里。
	pulled bool
	// blogUp 是 blog 项目已启动的服务数（0、1、2），Compose 那一幕逐个启动。
	blogUp int
}

// composeLabels 返回 compose 引擎给容器打的标签。
//
// 界面只读这两个：项目名用来在容器表里显示「项目」列、
// 在 Compose 页数「运行 n/m」。
func composeLabels(project, service string) string {
	return "com.wslc-compose.project=" + project + ",com.wslc-compose.service=" + service
}

// containersOf 按世界状态生成容器列表。
func containersOf(w world) []wslc.Container {
	state := func(on bool) (string, string) {
		if on {
			return "running", "Up 3 minutes"
		}
		return "exited", "Exited (0) 2 days ago"
	}
	mongoState, mongoStatus := state(w.mongoRunning)
	dbState, dbStatus := state(w.blogUp >= 1)
	ghostState, ghostStatus := state(w.blogUp >= 2)

	// 顺序是有意的：剧情里要操作的 dev-mongo 排在最前。开了日志面板之后表格只剩上半栏，
	// 排在后面就被挤出可见区域了。wslc 本来也是最近创建的在前。
	return []wslc.Container{
		{ID: "51f7c9a2d0b3", Names: mongoName, Image: "mongo:8", State: mongoState,
			Status: mongoStatus, Ports: portsIf(w.mongoRunning, "127.0.0.1:27017->27017/tcp")},
		{ID: "e8d3b1a49f60", Names: "adminer", Image: "adminer:4", State: "running",
			Status: "Up 5 hours", Ports: "0.0.0.0:8081->8080/tcp"},
		{ID: "9f3c1a7d2b4e", Names: "shop-web-1", Image: "nginx:1.27-alpine", State: "running", Health: "healthy",
			Status: "Up 2 hours (healthy)", Ports: "0.0.0.0:8080->80/tcp", Labels: composeLabels("shop", "web")},
		{ID: "2b6d84e0a1c7", Names: "shop-api-1", Image: "node:22-alpine", State: "running", Health: "healthy",
			Status: "Up 2 hours (healthy)", Ports: "0.0.0.0:3000->3000/tcp", Labels: composeLabels("shop", "api")},
		{ID: "c41e90b7d352", Names: "shop-db-1", Image: "postgres:17-alpine", State: "running", Health: "healthy",
			Status: "Up 2 hours (healthy)", Ports: "127.0.0.1:5432->5432/tcp", Labels: composeLabels("shop", "db")},
		{ID: "7a05f2c8e916", Names: "shop-cache-1", Image: "redis:7-alpine", State: "running",
			Status: "Up 2 hours", Ports: "6379/tcp", Labels: composeLabels("shop", "cache")},
		{ID: "a3902e6b7c14", Names: "blog-db-1", Image: "mysql:8.4", State: dbState,
			Status: dbStatus, Ports: "", Labels: composeLabels("blog", "db")},
		{ID: "0d6b4f1e83a9", Names: "blog-ghost-1", Image: "ghost:5-alpine", State: ghostState,
			Status: ghostStatus, Ports: portsIf(w.blogUp >= 2, "0.0.0.0:2368->2368/tcp"), Labels: composeLabels("blog", "ghost")},
	}
}

// portsIf 在条件成立时返回端口映射，否则为空：停止的容器不占端口。
func portsIf(on bool, ports string) string {
	if on {
		return ports
	}
	return ""
}

// imagesOf 按世界状态生成镜像列表。
func imagesOf(w world) []wslc.Image {
	list := []wslc.Image{
		{ID: "sha256:3f8d2a91c5e70b64", Repository: "nginx", Tag: "1.27-alpine", Size: "52.4MB", CreatedSince: "3 weeks ago", Containers: "1"},
		{ID: "sha256:b71c06e4d9a83f25", Repository: "node", Tag: "22-alpine", Size: "160MB", CreatedSince: "2 weeks ago", Containers: "1"},
		{ID: "sha256:94e5a3d18f02c7b6", Repository: "postgres", Tag: "17-alpine", Size: "278MB", CreatedSince: "2 weeks ago", Containers: "1"},
		{ID: "sha256:d20f6b8a47c13e95", Repository: "redis", Tag: "7-alpine", Size: "41.3MB", CreatedSince: "4 weeks ago", Containers: "1"},
		{ID: "sha256:6c4a1e97b2d80f35", Repository: "mongo", Tag: "8", Size: "877MB", CreatedSince: "3 weeks ago", Containers: "1"},
		{ID: "sha256:08b93d5e1a67c4f2", Repository: "mysql", Tag: "8.4", Size: "586MB", CreatedSince: "5 weeks ago", Containers: "1"},
		{ID: "sha256:fa17c2085d3e9b61", Repository: "ghost", Tag: "5-alpine", Size: "445MB", CreatedSince: "3 weeks ago", Containers: "1"},
		{ID: "sha256:2e85d7a04c19b3f6", Repository: "adminer", Tag: "4", Size: "101MB", CreatedSince: "6 weeks ago", Containers: "1"},
	}
	if w.pulled {
		// 新拉下来的放在最前面：刚完成的动作应该一眼看到结果。
		list = append([]wslc.Image{
			{ID: "sha256:7d1b30e4a8c59f26", Repository: "python", Tag: "3.13-slim", Size: "45.2MB", CreatedSince: "9 days ago", Containers: "0"},
		}, list...)
	}
	return list
}

// volumesOf 返回卷列表。
func volumesOf() []wslc.Volume {
	return []wslc.Volume{
		{Name: "shop_pgdata", Driver: "guest", Mountpoint: "/var/lib/docker/volumes/shop_pgdata/_data"},
		{Name: "blog_dbdata", Driver: "guest", Mountpoint: "/var/lib/docker/volumes/blog_dbdata/_data"},
		{Name: "blog_content", Driver: "guest", Mountpoint: "/var/lib/docker/volumes/blog_content/_data"},
	}
}

// networksOf 返回网络列表。
func networksOf() []wslc.Network {
	return []wslc.Network{
		{ID: "1c9e4b07a3d2f685", Name: "bridge", Driver: "bridge", CreatedAt: "2026-11-02 08:15:30 +0000"},
		{ID: "8a52d6f10e7b3c94", Name: "host", Driver: "host", CreatedAt: "2026-11-02 08:15:30 +0000"},
		{ID: "f4073b9c2e18a6d5", Name: "none", Driver: "null", CreatedAt: "2026-11-02 08:15:30 +0000"},
		{ID: "3d86a1f7c5042e9b", Name: "shop_default", Driver: "bridge", CreatedAt: "2026-11-10 09:12:44 +0000"},
		{ID: "b92e0c47d6f13a58", Name: "blog_default", Driver: "bridge", CreatedAt: "2026-11-12 14:03:19 +0000"},
	}
}

// projectsOf 返回 Compose 项目列表。
//
// 项目是「解析之后的配置」的精简投影（compose.ProjectConfig），这里直接手写，
// 不去读磁盘上的 compose 文件：演示不碰文件系统。
func projectsOf() []compose.Project {
	tcp := func(host string, container int) compose.PortMapping {
		return compose.PortMapping{Target: container, Published: host, Protocol: "tcp"}
	}
	return []compose.Project{
		{
			Dir: blogDir, Name: "blog",
			Config: compose.ProjectConfig{
				Name: "blog", Directory: blogDir,
				ComposeFiles: []string{blogDir + `\compose.yaml`},
				Services: map[string]compose.ServiceConfig{
					"db": {Name: "db", Image: "mysql:8.4", Replicas: 1,
						Volumes: []compose.VolumeMapping{{Type: "volume", Source: "blog_dbdata", Target: "/var/lib/mysql"}}},
					"ghost": {Name: "ghost", Image: "ghost:5-alpine", Replicas: 1,
						Ports:     []compose.PortMapping{tcp("2368", 2368)},
						DependsOn: []string{"db"},
						Volumes:   []compose.VolumeMapping{{Type: "volume", Source: "blog_content", Target: "/var/lib/ghost/content"}}},
				},
			},
		},
		{
			Dir: shopDir, Name: "shop",
			Config: compose.ProjectConfig{
				Name: "shop", Directory: shopDir,
				ComposeFiles: []string{shopDir + `\compose.yaml`},
				Services: map[string]compose.ServiceConfig{
					"web": {Name: "web", Image: "nginx:1.27-alpine", Replicas: 1,
						Ports:     []compose.PortMapping{tcp("8080", 80)},
						DependsOn: []string{"api"}},
					"api": {Name: "api", Image: "node:22-alpine", Replicas: 1,
						Ports:     []compose.PortMapping{tcp("3000", 3000)},
						DependsOn: []string{"cache", "db"}},
					"db": {Name: "db", Image: "postgres:17-alpine", Replicas: 1,
						Ports:   []compose.PortMapping{tcp("5432", 5432)},
						Volumes: []compose.VolumeMapping{{Type: "volume", Source: "shop_pgdata", Target: "/var/lib/postgresql/data"}},
						Health:  &compose.HealthcheckConfig{Test: []string{"CMD-SHELL", "pg_isready"}, Interval: "5s", Retries: 5}},
					"cache": {Name: "cache", Image: "redis:7-alpine", Replicas: 1},
				},
			},
		},
	}
}
