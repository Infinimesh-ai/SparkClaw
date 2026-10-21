# 接收设置操作恢复

语言：简体中文 | [English](../../docs/email-intake-recovery.md)

接收开关被中断后，保留原 ISCP 操作。检查连接只验证登录，不会创建邮箱或解除
原操作的未知状态。发信和草稿操作继续遵循原有未知结果规则。

`mail.intake.update` 将 deployment、owner、client、installation、原 operation ID
和原请求摘要绑定到邮箱事务。同一事务保存原邮箱结果和 provider binding version。
即使提交后的响应或最终收据丢失，仍可通过持久业务结果恢复原收据。

`mail.intake.reconcile` 是要求 `settings.write` 的显式 typed mutation，参数为
provider，正文为 `{operation_id, original}`。original 包含原 operation、params、body，
以及原有 expected_revision、request_id、input_digest。Gateway 必须先找到当前身份
scope 内的原收据，并核验精确请求摘要。恢复不会重做浏览器检查、发现、原设置请求、
发信或其他原始效果。`operations.receipt` 和所有 HTTP GET 均保持纯读。

若存在匹配的业务结果，恢复据此闭合原收据；否则，只有 provider binding version
仍等于原 expected version 时，才能在同一事务推进版本并保存 `not_applied` 负面结果。
持有旧版本的迟到写入会被 CAS 永久拒绝，包含不认识新恢复记录的旧 writer。
如果版本已经变化且缺少匹配的正面业务结果，则继续保持 unknown。此负面围栏也能
安全处理旧版未将邮箱事务关联到 operation ID 的 intake 收据。

恢复使用原操作作为幂等键，不再建立第二层 domain intent。恢复过程取消、崩溃或丢失
响应后，继续查询相同持久结果。迟到的原响应不能覆盖已闭合收据。Native 只对已协商
该能力、具有完整受保护原始意图的 intake 使用此路径；未知发信和仅有 fingerprint
的旧记录保持原语义。

同步状态新增 `provider_versions`，包含尚无 mailbox 的 provider；原接收开关使用
此版本。负面恢复提示上次更改未生效，之后新的明确前台操作可使用当前版本。
后台恢复不会产生替代操作，现有页面、控件、草稿和 mailbox 身份不变。

内存、文件和 PostgreSQL 使用相同 aggregate 转换。恢复结果使用现有邮件记录持久
存储，不随 domain 完成响应的保留期清除。软件回滚必须保留全部记录。恢复已过期的
响应时直接返回该业务证明，不重建已删除的响应正文，也不删除原重放围栏。若恢复已推进
空 binding 的版本，旧客户端或 Gateway 无法重新开启该 provider；旧 CAS 会安全失败，
直到重新升级。不能通过删除围栏让旧版本继续写入。
