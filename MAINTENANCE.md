# MacBox 维护笔记

本文档沉淀 2026-09 五阶段改造（CLI、自启拆分、宿主 Docker 复用、Apple container、实验性 mocker Compose 桥接）中得出的关键结论，供后续维护时参考。代码是唯一事实来源，与本文冲突时以代码为准。

## 1. 容器引擎路由（pkg/docker）

后端所有 Docker/Compose 操作统一走 `pkg/docker` 的路由，优先级为：

1. **宿主引擎**（OrbStack / Docker Desktop / `/var/run/docker.sock`）——检测到即复用，不再依赖 VM 内 Docker；
2. **Lima VM 内 Docker**（socket 探测）；
3. **limactl shell 兜底**。

探测结果缓存 5 秒（`engineProbeTTL`）。重要语义：命令返回 `*exec.ExitError` 说明 daemon 有响应（是业务错误），**不应**回退到 VM 路径；只有连接类错误才触发回退。`errors.As` 的判断链路在 `enrichCommandError` 包装后仍然保留（用 `%w` 包装），修改错误包装逻辑时不要破坏这一点。

`container.mode`（`auto|apple|docker`，存于 config.yaml）决定是否走 Apple `container` CLI 兜底；apple 模式下 Docker/Compose 端点要么由 mocker 桥接（见 §3），要么返回 501。

## 2. 配置热更新的两类陷阱

- **CLI 改配置 ≠ 运行中服务生效**：`macbox container mode`、`autostart` 等命令通过 `config.Update` 写文件，但正在运行的 Web 服务读的是启动时的内存快照。所有此类命令在健康检查通过时会打印 `macbox web restart` 提示，新增同类命令时保持这一行为。
- **`--no-open` 烧在 plist 里**：LaunchAgent 安装时将启动参数固化进 `~/Library/LaunchAgents/com.macbox.web.plist`，修改 `system.noOpen` 后必须重装 agent。`macbox autostart no-open on|off` 已内置这一刷新逻辑（检测到 agent 已加载时调用 `autostartEnable` 重新渲染 plist）。

自启动拆分为两个独立 LaunchAgent：`com.macbox.web`（Web 服务）与 `com.macbox.vm`（虚拟机），可分别 enable/disable，旧标签 `com.macbox.server` 仅作为迁移期兼容探测。

## 3. 实验性 mocker Compose 桥接（Apple container）

定位：**脆弱、可选、默认关闭的兼容层**，不是产品功能。mocker（第三方 AGPL CLI）把 `docker compose` 命令翻译到 Apple containerization 上执行。已知边界：

- `compose ls --format json` 在 mocker 下会静默输出表格而非 JSON——`listComposeProjects` 在该模式下**必须跳过** ls 调用，项目状态由容器状态推断（`composeStatusFromContainers`）；
- mocker 桥接下不能重写 compose 模板里的 `docker.sock` 挂载（会指向无关的宿主 Docker daemon），`TranslateComposeForEngine` 已按上下文抑制；
- 数据目录走宿主语义（`~/MacBox/data`），与 VM 模式的 `/data` **数据隔离**，两种模式互不可见；
- 依赖注入方式：HTTP 中间件 `composeEngine` 通过 `docker.WithComposeCLI(ctx, cliPath)` 按请求注入，`HostEngineActive`/`AppDataRoot` 等据此短路，调用点无感知。新增 compose 端点时记得套 `s.composeEngine(...)` 包装。
- 错误语义：非 Apple 引擎 → 直接放行；Apple + 桥接关 → 501（提示去 设置→实验性功能）；Apple + 桥接开但 mocker 未装 → 503（提示 `brew tap us/tap && brew install mocker`）。

出现问题优先怀疑上游 mocker，而不是在本项目里堆补丁（见 README 致谢节的定位声明）。

## 4. Lima 访客（guest）约定

- 复用宿主 Docker 的实例里 **guest 内没有 docker 组**是合法状态。所有涉及 `usermod -aG` 的逻辑必须先 `getent group` 守卫（VM 模板 provision 与运行时 `EnsureRuntimeAccess` 两处保持一致），否则会出现 `usermod: group 'docker' does not exist`。
- Lima 内部管理账号 `macboxctl` 需要 `docker`、`macbox` 附加组（存在才加）。
- 命令执行失败时通过 `enrichCommandError` 把 stderr 尾部（最后 4 行 / 400 字节）拼进 error，UI 侧不再只看到 `exit status 1`。

## 5. 诊断中心（/api/system/diagnostics）

Docker/引擎检查是**引擎感知**的：宿主复用模式下不得因"VM 内无 Docker"报警；apple 模式按 mocker / `sudo container system start` 状态给建议。新增引擎类型时同步更新 `handleSystemDiagnostics` 的分支，避免误报"虚拟机已运行，但 Docker 尚未就绪"。

## 6. 部署形态与二进制布局

- **前端已 `go:embed`**（`web/embed.go`），`bin/macbox` 单文件即可提供 Web UI（磁盘 `web/dist` 仅为开发回退）。
- **模板未嵌入**：`templates/vm/macbox.yaml.tmpl` 与 `templates/apps/*`（应用目录）运行时按 `projectRoot` 相对路径读盘。`detectProjectRoot` 依次在 可执行文件所在目录、其父目录、`../Resources` 找 `templates/`，因此二进制必须与 `templates/` 保持相对布局（如 `~/.local/share/macbox/bin/macbox` + `~/.local/share/macbox/templates`，DMG 则放 `Contents/Resources/templates`）。
- 想要**真正单文件分发**，需要把 templates 也 embed 并改造 vm/apps 的读盘路径（磁盘优先、嵌入回退），属于中等规模重构，尚未实施。
- 发行包由 `scripts/build-mac-release.sh`（tar.gz）与 `scripts/build-mac-dmg.sh`（DMG）打包；`install.sh` 安装到 `~/.local/share/macbox` 并软链 `~/.local/bin/macbox`。

## 7. CLI 与菜单栏的能力边界

两者都走同一 HTTP API/进程控制路径，CLI 额外覆盖：引擎模式切换（`container mode`）、Compose 桥开关（`container compose`）、日志（`web logs`）、打开控制台（`web open`）、自启细粒度控制（`autostart no-open` 等）。**卸载不在 CLI 范围内**：破坏性操作仅由菜单栏/`uninstall.sh` 承担，CLI 只打印指引。新增 CLI 命令时优先复用 `pkg/cli` 既有的 launchd/pid 辅助函数，不要另起进程管理逻辑。

## 8. Git 协作约定

- `origin` = 上游 `lulalulaluobo/macbox`，**永不推送**；
- `fork` = 个人仓库（推送目标）；
- 发版流程参照既有 tag（`v0.1.x`）：`make dmg` + `make release-mac` 产出后 `gh release create`。
