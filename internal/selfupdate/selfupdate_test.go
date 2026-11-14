package selfupdate

import (
	"errors"
	"sync/atomic"
	"testing"
)

// TestControllerSafeWithoutPlugin 验证插件没注册时，真实实现不会崩。
//
// 防的是这个缺陷：MyGo 的 updater 包在插件没注册时，每个函数都会 panic
// （"updater: the plugin is not used"）。如果界面直接调它，
// 单元测试、go run 这类从不注册插件的场景里，一打开系统页就会崩。
// 适配层要把「没注册」吸收掉：状态是零值，操作都是空操作。
func TestControllerSafeWithoutPlugin(t *testing.T) {
	registered.Store(false)
	c := New()

	st := c.Status()
	if st.Supported || st.Available != "" || st.AutoCheck || st.AutoInstall || !st.LastCheck.IsZero() {
		t.Errorf("没注册插件时状态应为零值（版本除外），实际 %+v", st)
	}

	// 下面每个调用在没有适配层时都会 panic。
	c.CheckNow()
	c.SetAutoCheck(true)
	c.SetAutoInstall(true)

	off := c.OnChange(func() { t.Error("没注册插件时不应该有任何变化通知") })
	if off == nil {
		t.Fatal("OnChange 必须返回可调用的取消函数，哪怕没注册插件")
	}
	off()
}

// TestMarkRegistered 验证只有 Setup 成功才算注册成功。
//
// 防的是这个缺陷：Setup 失败（比如已经注册过另一个更新插件）却把标志置位，
// 之后界面去调用没装上的插件，又回到上面那个 panic。
func TestMarkRegistered(t *testing.T) {
	t.Run("成功", func(t *testing.T) {
		var flag atomic.Bool
		if err := markRegistered(func() error { return nil }, &flag)(); err != nil {
			t.Fatalf("不该报错: %v", err)
		}
		if !flag.Load() {
			t.Error("Setup 成功后应置位")
		}
	})

	t.Run("失败", func(t *testing.T) {
		var flag atomic.Bool
		boom := errors.New("boom")
		if err := markRegistered(func() error { return boom }, &flag)(); !errors.Is(err, boom) {
			t.Fatalf("应把内层错误原样返回，实际 %v", err)
		}
		if flag.Load() {
			t.Error("Setup 失败时不能置位")
		}
	})

	t.Run("内层为空", func(t *testing.T) {
		var flag atomic.Bool
		if err := markRegistered(nil, &flag)(); err != nil || !flag.Load() {
			t.Errorf("Setup 为 nil 时也应能用：err=%v flag=%v", err, flag.Load())
		}
	})
}

// TestPluginIsNamedAndHasSetup 验证 Plugin 返回的是一个能交给 mygo.Use 的插件。
//
// 这里只看形状、不调用 Setup：Setup 会把插件登记成进程里唯一的更新插件，
// 登记之后无法撤销，会影响同一个测试进程里的其它用例。
func TestPluginIsNamedAndHasSetup(t *testing.T) {
	p := Plugin()
	if p.Name == "" {
		t.Error("插件需要名字")
	}
	if p.Setup == nil {
		t.Error("插件需要 Setup，注册标志靠它置位")
	}
}
