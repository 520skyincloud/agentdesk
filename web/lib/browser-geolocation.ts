export type BrowserCoordinates = {
  latitude: number
  longitude: number
  accuracy: number
}

class BrowserGeolocationError extends Error {
  constructor(
    readonly code: number,
    message: string,
  ) {
    super(message)
    this.name = "BrowserGeolocationError"
  }
}

function requestPosition(options: PositionOptions): Promise<BrowserCoordinates> {
  return new Promise((resolve, reject) => {
    let settled = false
    const settle = (callback: () => void) => {
      if (settled) return
      settled = true
      window.clearTimeout(watchdog)
      callback()
    }
    const watchdog = window.setTimeout(() => {
      settle(() => reject(new BrowserGeolocationError(3, "浏览器定位请求超时")))
    }, Number(options.timeout || 10000) + 1000)

    navigator.geolocation.getCurrentPosition(
      (position) => {
        settle(() => resolve({
          latitude: position.coords.latitude,
          longitude: position.coords.longitude,
          accuracy: position.coords.accuracy,
        }))
      },
      (error) => settle(() => reject(new BrowserGeolocationError(error.code, error.message))),
      options,
    )
  })
}

function toUserMessage(error: unknown) {
  const code = error instanceof BrowserGeolocationError ? error.code : 0
  if (code === 1) {
    return "浏览器没有定位权限，请在地址栏的网站设置中允许位置权限后重试"
  }
  if (code === 2) {
    return "浏览器定位服务当前不可用，可能受网络或系统定位服务影响。请在门店现场改用手机浏览器，或从地图复制经纬度手动填写"
  }
  if (code === 3) {
    return "获取坐标超时，请到开阔位置重试；仍失败时请从地图复制经纬度手动填写"
  }
  return error instanceof Error && error.message ? error.message : "获取坐标失败，请手动填写经纬度"
}

export async function getBrowserCoordinates(): Promise<BrowserCoordinates> {
  if (typeof window === "undefined" || !window.isSecureContext) {
    throw new Error("浏览器定位只能在 HTTPS 或本机地址中使用，请通过安全地址打开后台")
  }
  if (!navigator.geolocation) {
    throw new Error("当前浏览器不支持定位，请手动填写经纬度")
  }

  try {
    return await requestPosition({ enableHighAccuracy: true, timeout: 12000, maximumAge: 30000 })
  } catch (firstError) {
    const code = firstError instanceof BrowserGeolocationError ? firstError.code : 0
    if (code !== 2 && code !== 3) {
      throw new Error(toUserMessage(firstError))
    }
  }

  try {
    return await requestPosition({ enableHighAccuracy: false, timeout: 8000, maximumAge: 300000 })
  } catch (error) {
    throw new Error(toUserMessage(error))
  }
}
