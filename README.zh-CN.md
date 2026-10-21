# FastCI

[English](README.md) | [简体中文](README.zh-CN.md)

FastCI 把一个本地 git 仓库（**工作区 / Workspace**）复制到一个新建的 GitHub 仓库
（**目标仓库 / Target repo**），注入 GitHub Actions 工作流，再由这个工作流编译工作区里的
`Dockerfile`，把镜像推送到容器注册表。

你继续在自己的工作区里写代码。仓库、工作流、构建和标签都由 FastCI 负责。

## 安装

```sh
curl -fsSL https://raw.githubusercontent.com/Baiyuani/FastCI/main/install.sh | sh
```

`VERSION=v1.2.3` 指定版本，`INSTALL_DIR=...` 指定安装目录。Windows 用户可以从
[Releases](https://github.com/Baiyuani/FastCI/releases) 下载 `.zip`。开发 FastCI 本身：
`go build -o fastci ./cmd/fastci`。

## 前置条件

- `PATH` 里有 `git`。
- 一个带 `repo` 和 `workflow` 权限的 GitHub personal access token。`workflow` 权限正是
  允许 FastCI 写入 `.github/workflows/` 下文件的那个权限；没有它，推送会被拒绝。
- 本地**不需要** `docker`。镜像由 GitHub Actions 构建。

```sh
export GITHUB_TOKEN=ghp_...        # 或者写进下面的配置文件里的 "token"
```

## 快速开始

```sh
cd ~/src/my-app
fastci push
```

这会创建私有仓库 `<你的账号>/my-app`，注入 `.github/workflows/fastci-build.yml`，并推送
一个提交。这次推送触发构建，构建产出 `ghcr.io/<你的账号>/my-app`。

之后日常使用：

```sh
fastci update          # 把工作区复制到目标仓库，作为一个新提交
fastci status          # 上次同步和最近一次 CI 运行
fastci open --run      # 在浏览器里打开最新的工作流运行
```

## 命令

| 命令 | 作用 |
| --- | --- |
| `fastci push [工作区]` | 创建目标仓库，并把工作区同步进去 |
| `fastci update [工作区\|owner/repo]` | 再次同步工作区，产生一个提交 |
| `fastci list` | 列出本机创建过的所有目标仓库（`--json`） |
| `fastci status [工作区\|owner/repo]` | 单个目标仓库、上次同步、上次运行（`--json`） |
| `fastci open [工作区\|owner/repo]` | 在浏览器里打开仓库（`--actions`、`--run`） |
| `fastci delete [工作区\|owner/repo]` | 删除 GitHub 上的仓库并清除记录（`--yes`） |

每个命令都接受一个工作区目录，或一个 FastCI 认识的 `owner/repo`，默认使用当前目录。
退出码：成功是 `0`，失败是 `1`；`2` 表示命令行本身有问题。

`push` 和 `update` 接受 `--owner`、`--repo`、`--image`、`--dockerfile`、`--tags`、
`--workflow`、`--public`、`--registry-username`、`--registry-password`。`update` 还接受
`--force`。

## 配置

四个来源，后者覆盖前者：

1. 全局配置 `~/.config/fastci/config.json`（或用 `--config <路径>` 指定）：

   ```json
   {
     "token": "ghp_...",
     "owner": "my-account",
     "registry_username": "robot",
     "registry_password": "hunter2"
   }
   ```

   环境变量 `GITHUB_TOKEN` 覆盖 `token`。

2. 上次同步的记录，位于 `~/.config/fastci/state.json`。这个文件由 FastCI 写，所以
   `fastci update` 不需要把每个参数再写一遍。
3. 工作区里的 `fastci.json`，FastCI 会把它一起复制进目标仓库。它接受 `owner`、`repo`、
   `image`、`dockerfile`、`tags`、`workflow`、`public`。因为它会进入仓库，所以
   **凭据要放在全局配置里，绝不能放这里**：未知字段会直接报错。
4. 命令行参数。

FastCI 从不写入你的工作区。

## 一次同步会复制什么

`git ls-files -co --exclude-standard` 报告的全部内容，也就是已提交的文件，加上未提交但
没被忽略的文件。除此之外：

- `Dockerfile` 和 `fastci.json` 一定会带上，即使工作区忽略了它们。
- `.git`、嵌套仓库、submodule，以及 `.codegraph`、`.agents`、`.codex`、`.aws` 目录永远
  不会被复制。嵌套仓库会带一条警告被跳过，而不是被当成坏掉的 submodule 推上去。
- 软链接保持为软链接。
- 看起来像凭据的文件（`id_rsa*`、`*.pem`、根目录的 `.env`）会作为警告列出。它们仍然会被
  复制：这是你的判断，不是 FastCI 的。
- 单个文件超过 90 MB，或工作区总计超过 900 MB，会中止同步。

目标仓库里的 `.gitignore` 不会让任何文件逃过推送，所以即使工作区忽略了自己的
`Dockerfile`，目标仓库里也会有它。

## 更新，以及 FastCI 拥有什么

目标仓库是一份**镜像**：每次同步都让它和工作区完全一致。在工作区删掉文件，目标仓库里也会
删掉；`.fastci.json` 和工作流会按当前工具版本重新生成。

FastCI 从不做合并。如果目标仓库在 FastCI 之外被改动过（在 GitHub 网页上手动编辑、别人合
并了内容），`update` 会停下来让你先看看；`--force` 才会覆盖它。目标仓库的历史是生成的：
每次同步一个提交，message 是 `sync: <工作区> @ <提交>`。你工作区自己的提交永远不会被推
到那里。

## 镜像标签

每次同步都会把自己的标签渲染进工作流。默认是 `latest`，加上 `sha-<提交>`，其中 `<提交>`
是工作区的 HEAD 提交，而不是同步提交。配置了标签就**替换** `latest`；`sha-` 标签始终会被
追加，因为它是你唯一能拿工作区反查到的标签：

```sh
fastci push --tags v1,stable
# -> ghcr.io/me/app:v1, ghcr.io/me/app:stable, ghcr.io/me/app:sha-1a2b3c4
```

`sha-*` 记录的是 FastCI 当时看到的提交，所以从一个有未提交改动的工作区同步，产出的镜像
只对应到基线提交。

## 注册表

默认是 `ghcr.io`，构建时用本次运行自带的 `GITHUB_TOKEN` 登录，所以不需要任何配置。换别的
注册表时，把主机名写进镜像地址，并把凭据交给 FastCI：

```sh
fastci push --image registry.example.com/team/app \
  --registry-username robot --registry-password hunter2
```

这两项会成为 Actions secret `REGISTRY_USERNAME` 和 `REGISTRY_PASSWORD`（覆盖原有值），
并且永远不会写进工作流文件。最近一次的 `--tags` 和镜像设置会被记住，供 `update` 使用。

工作流只构建 `linux/amd64`。需要更多平台，就改
`.github/workflows/fastci-build.yml`；注意之后一次 `update` 会覆盖掉这个改动。

## 拉取私有镜像

目标仓库默认是私有的，对 GHCR 的首次推送会继承这个可见性，所以镜像也是私有的。拉取它需要
先登录：

```sh
echo "$GITHUB_TOKEN" | docker login ghcr.io -u <你的账号> --password-stdin
docker pull ghcr.io/<你的账号>/my-app:latest
```

`fastci delete` 之后镜像包仍然留在 GitHub Packages 里；FastCI 从不删除镜像，想删就到包的
设置里手动删。

## 开发

```sh
go test ./...   # 不联网、不需要凭据：假的 GitHub API 加临时仓库
go vet ./...
gofmt -l .
```

`GLOSSARY.md` 定义了项目的术语；`docs/adr/` 记录了上面这些行为背后的决策。
