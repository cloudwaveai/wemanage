// Plain-language reasons for failed requests, keyed by the gateway's error
// codes (and a few upstream ones). Shown next to the status in the usage log.
const REASONS: Record<string, string> = {
  device_unconfirmed: "新电脑待确认",
  device_blocked: "这台电脑已停用",
  account_rate_limited: "账号额度用完",
  usage_limit_reached: "账号额度用完",
  model_cooldown: "账号额度用完",
  account_needs_login: "账号登录失效",
  account_frozen: "Key 已暂停",
  employee_disabled: "账号已停用",
  no_account_bound: "还没分配 AI 账号",
  account_disabled: "AI 账号已停用",
  invalid_key: "Key 无效",
  missing_key: "没带 Key",
  network_not_allowed: "不在公司网络",
  client_not_allowed: "客户端不被允许",
  content_blocked: "触发使用规则",
  gateway_busy: "同时请求太多",
  gateway_unavailable: "网关暂时不可用",
  images_unsupported: "账号不支持生图",
  upstream_unreachable: "上游连不上",
  websocket_closed: "连接中断",
  context_length_exceeded: "对话超出长度",
};

export function failureReason(status: number, errorType: string): string {
  if (status >= 200 && status < 300) return "";
  const known = REASONS[errorType];
  if (known) return known;
  if (status === 499) return "已取消";
  if (status === 401 || status === 403) return "上游拒绝";
  if (status === 429) return "请求太频繁";
  if (status >= 500) return "上游出错";
  if (status >= 400) return "请求失败";
  return "";
}
