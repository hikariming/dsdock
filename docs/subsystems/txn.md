# 事务（`internal/txn`）

## 不变式

**所有改动 profile 的操作都必须经过 `txn.Engine.Run`。**新命令如果要改 profile，就构造一个 `txn.Change` 交给它，不要自己写文件或调用 `dsh plugin`。只读操作（`status`、`check`、`smoke`）不受此限制。

## 流程

```
Run(change)
 1. 拿 profile 的文件锁               <DSDOCK_HOME>/locks/<DSH_HOME 哈希>-<profile>.lock
 2. 记录基线：dsh --dump-config        基线组装失败也继续，但会记一条警告（救援场景需要）
 3. 拍快照 before                     基线正常时标为 verified，并记下 dump 的哈希
 4. change.Apply()                    非零退出码或报错 → 回滚
 5. 第一级验证：dsh --dump-config      失败、或 change.Expect 不通过 → 回滚
 6. 第二级验证（--smoke）：真正启动一次  DSH 退出、超时、或本次相关的插件没有激活 → 回滚
                                      其它插件没有激活只记警告（那是之前就有的问题）
 7. 拍快照 after，标为 verified
 8. 改了依赖的话，列出正在运行、需要重启的实例
```

## 回滚

1. 把 before 快照里的文件原子地写回去；快照里没有、但现在存在的受管文件会被删除；
2. `TouchesDeps` 为真时重装：先 `install --frozen-lockfile --offline`，失败再联网；
3. 如果 before 是 verified 的，重新组装一次，**哈希必须和 before 完全一致**，否则状态记为 `failed`，提示用户检查。

## 状态和退出码

| 操作记录的状态 | 含义 | CLI 退出码 |
|---|---|---|
| `applied` | 改动成功 | 0 |
| `rolled-back` | 改动失败，已经回到改动之前 | 3 |
| `failed` | 改动失败，回滚也没能完全恢复 | 4 |

装前检查拦下时退出码是 5（还没有开始事务），启动测试不通过是 6。

## 恢复快照

`Engine.Restore` 本身也是一次 `Run`：恢复之前会再拍一个快照，所以"恢复"也可以撤销。

## 还没做的

- DSH 运行中修改时，Windows 上可能遇到文件占用（等 Windows 的 M0 结果）；
- `$DSH_HOME/cordis.patch.yml`（全局层）目前不在快照范围内；
- 快照清理策略（设计文档 F4）。
