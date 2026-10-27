# Notes for coding agents

本项目目前处于 **M0 · 验证假设** 阶段，还没有产品代码。

- 如果你被要求做 M0 实测：读 [docs/m0-handoff.md](docs/m0-handoff.md)，严格遵守第 1 节的安全规则，结果写到 `docs/m0-results/<平台>.md`，并更新手册末尾的"接力进度"表。
- 修改设计之前，先读 [docs/design.zh-CN.md](docs/design.zh-CN.md)。实测结论推翻了某条【待实测】假设时，要同步更新设计文档 §2 的"状态"一列，以及受影响的章节。
- 不要读写用户真实的 `~/.dsh`。所有实验都用单独设置的 `DSH_HOME`。
