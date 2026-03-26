# Kocort API 参考

本文按路由分组说明 `api/routes.go` 中对外暴露的主要接口。它不是逐字段 schema 文档，而是面向开发者的“接口地图”。

## 1. API 表面分类

当前 HTTP 服务主要暴露七类接口：

1. `workspace`：聊天、历史、媒体、任务
2. `engine`：brain、capabilities、sandbox、data、技能导入
3. `integrations`：渠道配置与扫码接入
4. `system`：dashboard、audit、environment、network
5. `setup`：首次安装与引导
6. `rpc`：兼容型 RPC 接口
7. `channels/:channelID`：外部渠道入站 webhook

## 2. Workspace 接口

### 2.1 聊天

- `GET /api/workspace/chat/bootstrap`
  - 返回默认或指定 session 的初始化聊天数据
- `POST /api/workspace/chat/send`
  - 发送用户消息，进入 Runtime 聊天执行主链路
- `POST /api/workspace/chat/cancel`
  - 取消指定 run
- `GET /api/workspace/chat/history`
  - 分页获取聊天历史
- `GET /api/workspace/chat/events`
  - SSE 事件流；输出 token、tool、lifecycle、delivery 等事件

### 2.2 媒体与任务

- `GET /api/workspace/media`
  - 读取本地媒体文件给 WebChat 展示
- `GET /api/workspace/tasks`
  - 获取任务列表
- `POST /api/workspace/tasks`
  - 创建任务
- `POST /api/workspace/tasks/update`
  - 更新任务
- `POST /api/workspace/tasks/delete`
  - 删除任务
- `POST /api/workspace/tasks/cancel`
  - 取消任务

## 3. Engine 接口

这一组是管理面板的核心接口。

### 3.1 Brain 与模型

- `GET /api/engine/brain`
- `POST /api/engine/brain/save`
- `POST /api/engine/brain/models/upsert`
- `POST /api/engine/brain/models/delete`
- `POST /api/engine/brain/models/default`
- `POST /api/engine/brain/models/fallback`
- `POST /api/engine/brain/mode`

作用：

- 获取当前模型与 agent 状态
- 修改模型配置
- 设定默认 / fallback 模型
- 切换 cloud / local brain 模式

### 3.2 Cerebellum

- `POST /api/engine/brain/cerebellum/start`
- `POST /api/engine/brain/cerebellum/stop`
- `POST /api/engine/brain/cerebellum/restart`
- `POST /api/engine/brain/cerebellum/model`
- `POST /api/engine/brain/cerebellum/model/clear`
- `POST /api/engine/brain/cerebellum/model/delete`
- `POST /api/engine/brain/cerebellum/download`
- `POST /api/engine/brain/cerebellum/download/cancel`
- `POST /api/engine/brain/cerebellum/help`
- `POST /api/engine/brain/cerebellum/params`

### 3.3 BrainLocal

- `POST /api/engine/brain/local/start`
- `POST /api/engine/brain/local/stop`
- `POST /api/engine/brain/local/restart`
- `POST /api/engine/brain/local/model`
- `POST /api/engine/brain/local/model/clear`
- `POST /api/engine/brain/local/model/delete`
- `POST /api/engine/brain/local/download`
- `POST /api/engine/brain/local/download/cancel`
- `POST /api/engine/brain/local/params`

### 3.4 OAuth 与能力面板

- `POST /api/engine/brain/oauth/start`
- `POST /api/engine/brain/oauth/poll`
- `GET /api/engine/brain/oauth/status`
- `POST /api/engine/brain/oauth/logout`
- `GET /api/engine/capabilities`
- `POST /api/engine/capabilities/save`

### 3.5 技能、数据、沙箱

- `GET /api/engine/capabilities/skill/files`
- `GET /api/engine/capabilities/skill/file`
- `POST /api/engine/capabilities/skill/install`
- `POST /api/engine/capabilities/skill/import/validate`
- `POST /api/engine/capabilities/skill/import/confirm`
- `POST /api/engine/capabilities/skill/browse-dir`

- `GET /api/engine/data`
- `POST /api/engine/data/save`

- `GET /api/engine/sandbox`
- `POST /api/engine/sandbox/save`

## 4. Integrations 接口

- `GET /api/integrations/channels`
- `POST /api/integrations/channels/save`
- `POST /api/integrations/channels/weixin/qr/start`
- `POST /api/integrations/channels/weixin/qr/poll`

用途：

- 查看和保存渠道配置
- 发起特定渠道的扫码登录流程

## 5. System 接口

- `GET /api/system/dashboard`
- `POST /api/system/audit/list`
- `GET /api/system/environment`
- `POST /api/system/environment/save`
- `POST /api/system/environment/reload`
- `GET /api/system/network`
- `POST /api/system/network/save`

这一组主要服务管理面板的系统页。

## 6. Setup 接口

- `GET /api/setup/status`
- `POST /api/setup/complete`

用于首次启动后的初始化向导状态管理。

## 7. RPC 接口

RPC 路径保留了更轻量的兼容入口：

- `GET /healthz`
- `POST /rpc/chat.send`
- `POST /rpc/chat.cancel`
- `GET /rpc/chat.history`
- `GET /rpc/chat.events`
- `GET /rpc/dashboard.snapshot`
- `POST /rpc/audit.list`

这些接口和 `workspace/system` 接口并非两套后端，而是对同一 Runtime 能力的不同包装。

## 8. 渠道入站

- `POST /channels/:channelID`

这个路由会把请求交给 `ChannelManager` 中对应 adapter 的 `ServeHTTP`。不同渠道自行解释 webhook payload，但最终目标仍是把消息送入统一 Runtime 流程。

## 9. 静态资源与 WebChat

如果 webchat 启用，Gateway 还会注册静态资源路由并承载前端页面。

在调试或兼容场景下，`handlers/rpc.go` 还内置了一个简单的 HTML WebChat 页面。

## 10. 返回风格与错误处理

当前 API 风格较统一：

- 成功：直接返回业务 JSON 结构
- 参数问题：通常 `400`
- 未找到：`404`
- 运行失败：`400` 或 `500`，并在 JSON 中附上 `error`

文档化或补 SDK 时建议继续沿用这套规则，而不是引入另一套 envelope。
