# 对外演讲 PPT

- `AI订阅网关方案汇报.pptx` —— 12 页可编辑 PowerPoint，用于对外/管理层汇报。中文字体为微软雅黑（在 Windows/WPS/装有该字体的 PowerPoint 中正常显示）。含每页演讲备注。
- `deck.js` —— 生成脚本（pptxgenjs）。重生成：在含 pptxgenjs 的环境执行 `node deck.js "AI订阅网关方案汇报.pptx"`。

## 技术版

- `AI订阅网关技术方案.pptx` —— 14 页技术版，给工程师看：架构、数据面生命周期、并发闸、日志三层存储、片段去重+前缀链、**本次可插拔 Blob 后端改动（Postgres|S3/R2）**、指纹、数据模型、部署、测试。
- `deck-tech.js` —— 技术版生成脚本。重生成：`node deck-tech.js "AI订阅网关技术方案.pptx"`。
