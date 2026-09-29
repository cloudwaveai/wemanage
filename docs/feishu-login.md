# 飞书登录设计与配置

本系统**唯一登录入口就是飞书**（生产环境不开放开发登录）。本文说明：飞书应用怎么建、登录流程怎么走、第一个管理员怎么进来、员工怎么被放行。

## 一、登录流程（浏览器视角）

```
员工点“飞书登录”
   │  GET /api/auth/feishu/login
   ▼
网关生成随机 state 存库，302 跳转到
   https://accounts.feishu.cn/open-apis/authen/v1/authorize
     ?client_id=<App ID>&redirect_uri=<回调>&response_type=code&state=<state>
   │  员工在飞书授权
   ▼
飞书带 code 回跳
   GET /api/auth/feishu/callback?code=..&state=..
   │  网关校验并消费 state
   ├─ POST https://open.feishu.cn/open-apis/authen/v2/oauth/token
   │     {grant_type, client_id, client_secret, code, redirect_uri}  → 拿到 user_access_token
   ├─ GET  https://open.feishu.cn/open-apis/authen/v1/user_info
   │     Authorization: Bearer <user_access_token>                    → 拿到 open_id / 姓名 / 头像
   ├─ 用 open_id 在员工表里找人：找到且在职 → 建会话、下发 Cookie，进入系统
   │                              找不到 → 回登录页，提示「账号还没开通，请联系管理员同步通讯录」
   ▼
进入门户（可直接领 Key，不需要先确认任何告知）
```

**在飞书里打开时不用点按钮。** 门户检测到自己在飞书客户端里（工作台、机器人消息里的链接），会自动走上面的登录流程；员工已经登录了飞书，通常一闪就进去了。登录后回到链接原本指向的页面（比如消息里的"我的设备"链接），不会落到首页。手动退出登录后，这个标签页里不再自动登录。登录失败（没开通、已停用、链接过期）时回到登录页并显示原因，而不是一段 JSON。

要点：
- **凭 open_id 认人**，不是邮箱/手机号。open_id 是「这个飞书应用内」对某个用户的稳定唯一标识。
- 员工的姓名/部门等信息来自**通讯录同步**（见第四节），不是每次登录现取。所以登录前必须先同步过通讯录，用户才存在、才能进。
- `state` 一次性、存库、用完即删，防 CSRF。

## 二、在飞书开放平台建应用（一次性）

在 https://open.feishu.cn/app 操作，需要飞书**企业管理员**配合审批。

1. **创建企业自建应用**，在「凭证与基础信息」里拿到 **App ID**（`cli_` 开头）和 **App Secret**。
2. **添加应用能力 → 机器人**。网关用应用身份给员工和管理员发飞书消息（新设备确认、冻结通知、账号告警、周报），没有机器人能力发不出去。
3. **安全设置 → 重定向 URL** 加上：
   ```
   https://<你的网关域名>/api/auth/feishu/callback
   ```
   必须和 `.env` 里的 `PUBLIC_BASE_URL` 拼出来的地址**完全一致**（协议、域名、路径都一样）。
4. **权限管理 → 开通权限**（在搜索框里直接搜下面的权限标识）：

   | 权限标识 | 用途 | 必需 |
   |---|---|---|
   | `contact:contact.base:readonly` | 读通讯录（列部门、列部门成员） | 是 |
   | `contact:department.base:readonly` | 部门名称 | 是 |
   | `contact:department.organize:readonly` | 部门负责人（决定谁能看本部门的团队页） | 是 |
   | `contact:user.base:readonly` | 员工姓名、头像 | 是 |
   | `contact:user.department:readonly` | 员工属于哪些部门 | 是 |
   | `contact:user.employee:readonly` | 员工状态（离职、冻结的飞书账号同步后停用） | 建议 |
   | `contact:user.email:readonly` | 员工邮箱（仅展示） | 可选 |
   | `im:message:send_as_bot` | 以应用身份发消息 | 是 |

   飞书登录本身（换 token、取 open_id）不需要额外权限。
5. **权限管理 → 数据权限 → 通讯录权限范围**：设为**全部成员**。同步是从根部门往下递归列的，范围不是全员就拿不到根部门，同步会失败或漏人。
6. **版本管理与发布 → 可用范围**：设为**全部成员**（至少覆盖所有要用网关的人）。不在可用范围里的人收不到机器人消息，通常也无法授权登录。
7. **添加应用能力 → 网页应用**（推荐）：桌面端主页和移动端主页都填 `https://<你的网关域名>/`。发布后员工在飞书「工作台」里点开就是门户，不用记网址。企业管理员还可以在飞书管理后台「工作台 → 工作台设置」把它加进常用应用，全员可见。
8. **事件与回调 → 回调配置**（推荐）：订阅方式选"将回调发送至开发者服务器"，请求地址填 `https://<你的网关域名>/api/feishu/card`，保存（飞书会立即校验这个地址），再点"添加回调"，勾选**卡片回传交互**（`card.action.trigger`）。这样员工收到的新设备提醒是带"是我 / 不是我"按钮的卡片，点一下就处理完，不用再打开门户。网关收到飞书的地址校验后自动改发卡片；没配之前照旧发带链接的文字消息。
   - 同一页的 **Verification Token** 可以填进 `.env` 的 `FEISHU_VERIFICATION_TOKEN`，网关会据此核对回调来自飞书（可选：按钮本身带网关签发的加密令牌，别人伪造不了）。
   - 如果在"加密策略"里设了 **Encrypt Key**，必须同时填进 `.env` 的 `FEISHU_ENCRYPT_KEY`，否则网关解不开回调；不设则不用填。
9. **创建版本并发布**，由企业管理员审批通过后，上面的能力和权限才生效。以后改权限、改可用范围、加应用能力，都要重新发版本。
10. 把凭证填进网关 `.env`：
   ```
   FEISHU_APP_ID=cli_xxx
   FEISHU_APP_SECRET=xxx
   FEISHU_BASE_URL=https://open.feishu.cn
   PUBLIC_BASE_URL=https://<你的网关域名>     # 决定回调地址，必须和重定向 URL 一致
   BOOTSTRAP_SUPER_ADMINS=ou_xxx             # 你自己的 open_id，见第三节
   FEISHU_VERIFICATION_TOKEN=                # 可选，见第 8 步
   FEISHU_ENCRYPT_KEY=                       # 只在飞书后台设了 Encrypt Key 时填
   ```

**同步用到的接口**（便于排查权限报错）：`GET /open-apis/contact/v3/departments/0/children?fetch_child=true` 递归列部门；对根部门和每个部门调 `GET /open-apis/contact/v3/users/find_by_department` 列直属成员并去重；ID 一律用 `open_department_id` 和 `open_id`。**如果一个人都没同步到，网关会直接报错、不会停用任何员工**，这种情况几乎都是通讯录权限范围或权限没开。

> 国际版 Lark：`FEISHU_BASE_URL=https://open.larksuite.com`，授权域名会自动切到 `accounts.larksuite.com`。

## 三、第一个管理员怎么进来（关键：先有鸡还是先有蛋）

系统要求“登录者必须已在员工表里”，但第一次部署时员工表是空的，通讯录也还没同步。解决办法是**用飞书 open_id 预置超级管理员**：

1. 先拿到你自己的飞书 **open_id**。两种方式任选：
   - **用 API 查**（在能连飞书的机器上，替换 App 凭证和你的手机号/邮箱）：
     ```bash
     TOKEN=$(curl -s -X POST https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal \
       -H 'Content-Type: application/json' \
       -d '{"app_id":"cli_xxx","app_secret":"xxx"}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["tenant_access_token"])')
     curl -s -X POST "https://open.feishu.cn/open-apis/contact/v3/users/batch_get_id?user_id_type=open_id" \
       -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
       -d '{"mobiles":["你的手机号"]}'
     # 返回里的 user_list[].user_id 就是 open_id（ou_ 开头）
     ```
   - 或部署后先不放行，看网关日志：你登录失败那次，日志里会带上你的 open_id（登录被拒也解析了身份）。
2. 把这个 open_id 填进 `.env`：
   ```
   BOOTSTRAP_SUPER_ADMINS=ou_你的id[,ou_第二个管理员,...]
   ```
3. 网关启动时会自动把这些 open_id 建成**超级管理员**（`gateway admin bootstrap` 也可手动执行）。
4. 现在你能登录了 → 进后台点 **“同步飞书”** → 全公司员工进入员工表 → 其他人就能登录了。
5. 以后要增减超级管理员，到后台「员工」页最后一列的「超级管理员」操作即可，立即生效并记审计。有两个限制：
   - 不能改自己的身份，保证至少留一个管理员；
   - `BOOTSTRAP_SUPER_ADMINS` 里的人每次启动都会被重新设为管理员，页面上显示「配置指定」，不能在页面上取消，要先从 `.env` 里去掉再重启。

网关的权限**不跟飞书的企业管理员走**：超级管理员由上面两处决定；部门负责人跟飞书通讯录里的「部门负责人」走，只能看本部门（含下级部门）的总用量和成员账号是否正常，看不到成员的对话和个人用量；每个人都能看自己的用量和对话记录。

## 四、谁能登录（放行规则）

| 情况 | 结果 |
|---|---|
| open_id 在员工表、状态在职 | 放行 |
| open_id 在 `BOOTSTRAP_SUPER_ADMINS` | 放行（超级管理员，且可先于同步进入） |
| open_id 不在员工表 | 回登录页，提示联系管理员同步通讯录 |
| 员工已离职（飞书停用 → 同步后置为 disabled） | 回登录页，提示账号已停用 |

同步是**每小时自动**跑一次，也可在后台手动点“同步飞书”。飞书里离职的人，同步后自动停用、Key 失效。

## 五、测试期注意（重要）

飞书回调要求 HTTPS/可信域名，`http://IP:8080` 通常不被接受。验证阶段有两种做法：

- **推荐**：给验证服务器绑一个域名 + HTTPS（用 Caddy 自动证书，或放在能签证书的入口后面），`PUBLIC_BASE_URL=https://域名`，回调白名单填同样地址。这样飞书登录能真跑通。
- **临时**：先用**开发登录**（`DEV_LOGIN=true`，仅本地/内网测试用）验证系统其他部分（发 Key、并发、日志、看板），飞书登录等有域名了再联通。生产务必 `DEV_LOGIN=false`。

## 六、涉及的代码位置

- 授权跳转与回调：[internal/web/api_auth.go](../internal/web/api_auth.go)
- 新设备卡片与按钮回调：[internal/devicecard/devicecard.go](../internal/devicecard/devicecard.go)、[internal/web/feishu_card.go](../internal/web/feishu_card.go)
- 飞书 OAuth 客户端（authorize / v2 token / user_info）：[internal/feishu/client.go](../internal/feishu/client.go)
- 通讯录同步（部门/员工/离职停用）：[internal/feishu/sync.go](../internal/feishu/sync.go)
- 会话 Cookie 与 CSRF：[internal/web/session.go](../internal/web/session.go)
- 首个管理员种子：`gateway admin bootstrap` / `BOOTSTRAP_SUPER_ADMINS`（[cmd/gateway/admin.go](../cmd/gateway/admin.go)）
