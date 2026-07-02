# Reasonix 插件包

Reasonix 插件包把 skills、hooks 和 MCP servers 组织成一个可安装单元。

## 安装

```bash
reasonix plugin install git:github.com/obra/superpowers --yes
```

只预览计划，不写文件：

```bash
reasonix plugin install git:github.com/obra/superpowers --dry-run
```

本地开发：

```bash
reasonix plugin install /path/to/plugin --link --yes
```

插件状态和内容写入：

```text
~/.reasonix/plugin-packages.json
~/.reasonix/plugins/<name>/
```

## 桌面端设置

打开 **设置 -> 插件**，可以不用 CLI 直接安装和管理插件包。

### 安装插件

安装区有两种模式：

- **本地目录**：点击 **选择插件目录**，从磁盘选择一个插件目录。
  选中路径会显示在按钮右侧。
- **Git 仓库**：填写 Git 来源，例如 `git:github.com/obra/superpowers`。
  **安装名称（可选）** 可覆盖插件 manifest 声明的名称，用于本次安装或覆盖。

选择来源和选项后，再使用操作按钮：

- **预检** 校验来源并展示计划安装动作，不写入文件。
- **安装插件** 按当前来源和选项执行安装。
- **刷新插件** 从磁盘和配置重新读取已安装插件列表。

安装选项：

- **覆盖同名插件** 允许当前来源替换已安装的同名插件。关闭时，同名安装会失败，
  而不是覆盖已有内容。
- **开发模式：链接源目录** 只在 **本地目录** 模式出现。它不会复制插件，
  而是直接链接所选目录；适合开发或调试插件。移动或删除该目录会导致这个链接插件失效。

对新的 Git 来源或本地插件目录，建议先点 **预检**。

### 管理已安装插件

已安装插件列表会展示每个插件包以及它导出的 skills、hooks 和 MCP servers。
通过应用外编辑插件文件或配置后，可点 **刷新插件** 重新读取。

展开插件行后可以：

- 启用或禁用插件。
- 使用 **更新** 拉取或刷新具备更新来源的插件。
- 使用 **诊断** 检查插件 manifest，并查看警告或诊断信息。
- 使用 **移除插件**，确认后卸载该插件包。

## 原生 Manifest

Reasonix 原生插件在根目录声明 `reasonix-plugin.json`：

```json
{
  "name": "example",
  "version": "1.0.0",
  "description": "Example plugin",
  "skills": "skills",
  "hooks": {
    "SessionStart": [
      {
        "command": "hooks/session-start",
        "description": "Load startup context"
      }
    ]
  },
  "mcpServers": {
    "helper": {
      "command": "bin/helper"
    }
  }
}
```

相对路径都按插件根目录解析。Reasonix 安装插件时不会执行第三方安装脚本。

## Codex 兼容

Reasonix 也会读取 `.codex-plugin/plugin.json`。对于 Superpowers 这类插件，
Reasonix 会映射：

- `skills` 到 Reasonix skill root。
- 如果存在 `hooks/session-start-codex`，映射为 Reasonix `SessionStart` hook。

插件 hook 会收到这些环境变量：

- `REASONIX_PLUGIN_ROOT`
- `REASONIX_PLUGIN_NAME`
- `REASONIX_PLUGIN_VERSION`
- `REASONIX_HOME`
- `REASONIX_WORKSPACE_ROOT`

## 管理命令

```bash
reasonix plugin list
reasonix plugin show superpowers
reasonix plugin doctor superpowers
reasonix plugin disable superpowers
reasonix plugin enable superpowers
reasonix plugin remove superpowers --yes
```

Desktop 后端暴露同等 Wails 方法：

- `Plugins`
- `PlanPluginInstall`
- `InstallPlugin`
- `RemovePlugin`
- `SetPluginEnabled`
- `UpdatePlugin`
- `PluginDoctor`
