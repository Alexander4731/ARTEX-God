export interface CurrentUser {
  id: string;
  name: string;
  username: string;
  email: string;
  avatar: string;
  role: string;
}

export const auth = {
  async check(): Promise<boolean> {
    if (process.env.NEXT_PUBLIC_MOCK === "1") return true;
    localStorage.removeItem("artex_token");
    const response = await fetch("/api/auth/status", { cache: "no-store" });
    if (!response.ok) throw new Error("无法检查登录状态");
    return (await response.json()).authenticated === true;
  },

  async clearToken(): Promise<void> {
    localStorage.removeItem("artex_token");
    const response = await fetch("/api/auth/logout", { method: "POST" });
    if (!response.ok) throw new Error("退出登录失败");
  },

  getCurrentUser(): CurrentUser {
    return { id: "1", name: "ARTEX", username: "ARTEX", email: "", avatar: "", role: "operator" };
  },
};
