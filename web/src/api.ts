export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  let response: Response;
  try {
    response = await fetch(path, {
      ...init,
      headers: {
        "Content-Type": "application/json",
        ...(init?.headers ?? {})
      }
    });
  } catch {
    throw new Error("服务连接失败，请确认程序后端已启动。");
  }
  const text = await response.text();
  const payload = parsePayload(text);
  if (!response.ok) {
    throw new Error(errorMessage(response, payload));
  }
  return payload as T;
}

function parsePayload(text: string) {
  if (!text) return undefined;
  try {
    return JSON.parse(text);
  } catch {
    return { error: text.trim(), raw: true };
  }
}

function errorMessage(response: Response, payload: unknown) {
  const isPlainText = typeof payload === "object" && payload && "raw" in payload;
  const error = typeof payload === "object" && payload && "error" in payload ? String(payload.error) : "";
  if (isPlainText && response.status >= 500) return "服务暂不可用，请稍后重试或导出诊断报告。";
  if (error) return error;
  if (response.status >= 500) return "服务暂不可用，请稍后重试或导出诊断报告。";
  return response.statusText || `请求失败 (${response.status})`;
}

export function query(params: Record<string, string>): string {
  const search = new URLSearchParams(params);
  return search.toString();
}
