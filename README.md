# RQAMS CLI

`rqamsc` 是面向终端脚本和 AI Agent 的 RQAMS 命令行工具。所有业务命令统一使用 JSON payload 输入，默认返回 JSON envelope，列表类命令可按需输出 NDJSON。

```powershell
rqamsc <verb> <resource> --payload <json|@file|->
```

## 文档分工

| 文档 | 用途 |
| --- | --- |
| [使用手册](docs/rqams_cli_manual.md) | 完整文档入口：安装方式、通用协议、命令导航、业务文档索引和高频示例 |

仓库根目录的 `README.md` 描述 Go/npm 形态的 `rqamsc` CLI。详细业务说明、payload 字段、返回结构和示例维护在 `docs/commands/` 目录。

## 安装与构建

### 通过 npm 安装

用户只需要安装主包，npm 会按当前平台自动安装对应的可选平台二进制包：

```powershell
npm install -g @ricequant2026/rqams-cli
rqamsc --version
rqamsc setup
```

如果安装后提示缺少平台包，通常是 optional dependencies 被禁用，可以显式开启后重新安装：

```powershell
npm install -g @ricequant2026/rqams-cli --include=optional
```

### 从源码构建

本项目使用 Go 1.22+。本地直接运行：

```powershell
go run ./cmd/rqamsc --version
go run ./cmd/rqamsc schema list
```

构建当前平台二进制：

```powershell
go build -trimpath -ldflags "-s -w -X rqams-cli/internal/app.Version=0.0.2" -o rqamsc.exe ./cmd/rqamsc
.\rqamsc.exe --version
```

非 Windows 平台可把输出文件名改为 `rqamsc`。

### 交叉编译

Windows x64：

```powershell
$env:CGO_ENABLED = "0"
$env:GOOS = "windows"
$env:GOARCH = "amd64"
go build -trimpath -ldflags "-s -w -X rqams-cli/internal/app.Version=0.0.2" -o dist/rqamsc-windows-amd64.exe ./cmd/rqamsc
```

Linux x64：

```powershell
$env:CGO_ENABLED = "0"
$env:GOOS = "linux"
$env:GOARCH = "amd64"
go build -trimpath -ldflags "-s -w -X rqams-cli/internal/app.Version=0.0.2" -o dist/rqamsc-linux-amd64 ./cmd/rqamsc
```

macOS arm64：

```powershell
$env:CGO_ENABLED = "0"
$env:GOOS = "darwin"
$env:GOARCH = "arm64"
go build -trimpath -ldflags "-s -w -X rqams-cli/internal/app.Version=0.0.2" -o dist/rqamsc-macos-arm64 ./cmd/rqamsc
```

`npm run build:npm` 会执行同样的交叉编译流程，并用 `package.json` 里的版本号注入二进制。

### npm 包结构

本仓库通过 npm 分发 Go 二进制，采用“主包 + 平台包”的结构：

- 主包 `@ricequant2026/rqams-cli` 维护 JS launcher、文档和构建脚本。
- 平台包 `@ricequant2026/rqams-cli-<platform>` 保存对应平台的 Go 二进制。
- `npm/` 和 `dist/` 是 `npm run build:npm` 生成的发布产物，不需要手工维护。

构建所有平台 npm 包：

```powershell
npm run build:npm
```

链接当前平台包用于本地测试：

```powershell
npm run link:platform
npm link
```

验证命令：

```powershell
rqamsc --version
rqamsc setup
rqamsc schema list
rqamsc get product-list --payload '{}'
```

打包发布产物：

```powershell
npm run pack:all
```

发布到 npm：

```powershell
npm run publish:dry-run
npm run publish:all
```

`publish:all` 会先生成平台包，按平台包优先、主包最后的顺序发布。主包的 `optionalDependencies` 指向平台包，用户只需要安装主包：

```powershell
npm install -g @ricequant2026/rqams-cli
```

## 功能范围

- 统一命令入口：`rqamsc <verb> <resource> --payload ...`
- 命令发现：`--help`、`--version`、`schema list`、`schema get`
- 市场数据查询：交易日历等只读数据
- Workspace、产品、产品组管理
- 交易流水、估值表、托管事件、份额事件
- 自定义合约、自定义基准、自定义指标
- 指标、投资概览、绩效归因、交易分析
- 模拟交易创建、配置、信号上传、重算和删除
- 文件上传和下载使用本地路径，返回保存路径或任务结果
- npm 分发采用主包提供 JS launcher、平台包提供 Go 二进制的方式

## Agent 集成

`rqamsc` 提供运行时 schema，Agent 可以用它发现命令、参数和返回结构：

```powershell
rqamsc schema list
rqamsc schema get --payload '{"command":"get product-list"}'
```

更完整的 skill 或 Agent 集成说明维护在 [ricequant-skills](https://github.com/ricequant/ricequant-skills/tree/main/skills/basic/rqams)。

## 配置

本地状态默认保存在操作系统用户配置目录的 `rqams-cli/config.json`。用户执行 `auth` 和 `use workspace` 后，后续命令会自动复用本地配置，不需要设置环境变量。
`auth` 和 `setup` 的 `username` 如果是 11 位中国大陆手机号，CLI 会自动补 `+86` 后登录并保存；已经包含区号、邮箱或普通用户名会保持原样。

当前 CLI 会把密码和登录 session 明文写入本地配置。session 过期时，CLI 会用本地保存的密码自动重新登录并刷新 session。若用于生产凭据长期保存，需要改为系统 keychain 或等价安全存储。

单账号单 workspace 可以直接使用默认配置。需要单账号多 workspace 或多账号多 workspace 并行使用时，建议为每个上下文设置独立 profile：

```powershell
rqamsc auth --payload '{"profile":"acct-a-w1","base_url":"https://www.ricequant.com","username":"...","password":"..."}'
rqamsc use workspace --payload '{"profile":"acct-a-w1","workspace_name_or_id":"workspace-a"}'
rqamsc get product-list --payload '{"profile":"acct-a-w1"}'
```

业务命令可以在 payload 顶层传 `profile`，从同一个配置文件中选择互相隔离的登录态和 workspace。
