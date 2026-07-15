import { request } from "@/lib/api/client"

export function deleteAsset(id: number) {
  return request<void>("/api/dashboard/asset/delete", {
    method: "POST",
    body: JSON.stringify({ id }),
  })
}
