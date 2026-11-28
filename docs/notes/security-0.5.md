# ZeroDesk 0.5: sign-in and security changes

[中文](#中文) | **English**

ZeroDesk 0.5 changes how browsers sign in. If you signed in before, connect again once.

## Why

Up to 0.4, every browser signed in with the same device access token, kept in a cookie:

- Signing out only removed the cookie from that browser. The device never forgot it, so a copied cookie kept working.
- Pairing was approved by pressing Enter on the CardputerZero, whoever had asked. Anyone on the same Wi-Fi could start a request, and one stray Enter gave them full access: Terminal, files, administrator actions.
- The device answered to any host name, so a web page on another site could reach it through your browser.

## What changed

- **Each browser has its own sign-in.** Settings › General › Signed-In Browsers lists them. Sign out one, or all of them at once (this also replaces the access token).
- **Pairing asks for the code.** The browser shows four digits; type them on the CardputerZero keyboard and press OK. If you prefer, Settings › General › Confirm Connections › Press OK shows the same four digits on the device instead, to compare before pressing OK. Either way the device shows which address is asking. One request waits at a time, and repeated requests from one address are slowed down.
- **ZeroDesk answers only to its own names**: the device's address, `<name>.local`, and names you add in Settings › General › Names (for a reverse proxy or your own domain). Requests that change something must come from ZeroDesk's own pages.
- **Signing out closes what that browser had open**, such as Terminal windows and web apps.
- **Files that would run as a web page** (HTML, SVG, scripts) are downloaded instead of opened.
- **App Store**: a package without a checksum is not installed, and the checksum is checked again just before installing. Administrator rights are used only for the install step.
- **Zagent's page (`zagent.local`)** needs a ZeroDesk sign-in.

## What you need to do

Open ZeroDesk and connect again: type the code shown in the browser on the CardputerZero, then press OK. Or use the access token. Scripts that send the access token in the `X-ZeroDesk-Token` header keep working.

Connections are still plain HTTP on your local network. HTTPS for local connections is planned.

---

## 中文

ZeroDesk 0.5 改变了浏览器的登录方式。之前登录过的浏览器需要重新连接一次。

### 为什么

0.4 及以前，所有浏览器都用同一个设备访问令牌登录，保存在 cookie 里：

- 退出登录只删掉这个浏览器里的 cookie，设备本身并不知道，被复制走的 cookie 依然有效。
- 配对时，只要在 CardputerZero 上按回车就算同意，不管是谁发起的请求。同一个 Wi-Fi 里的任何人都能发起请求，一次误按回车就会让对方拿到全部权限：终端、文件、管理员操作。
- 设备对任何主机名都应答，其他网站的网页可以借你的浏览器访问它。

### 改了什么

- **每个浏览器单独登录。** 在「设置 › 通用 › 已登录的浏览器」里可以看到全部，可以单独退出，也可以一次全部退出（同时更换访问令牌）。
- **配对需要输入连接码。** 浏览器上显示 4 位数字，在 CardputerZero 的键盘上输入后按 OK。也可以在「设置 › 通用 › 连接确认方式」中改为「按 OK」：设备屏幕上显示同样的 4 位数字，核对一致后按 OK。两种方式下，设备屏幕都会显示发起请求的地址。同一时间只处理一个请求，同一地址反复发起会被限速。
- **只应答 ZeroDesk 自己的名称**：设备的地址、`<名称>.local`，以及在「设置 › 通用 › 名称」里添加的名称（用于反向代理或自己的域名）。会改动设备的请求必须来自 ZeroDesk 自己的页面。
- **退出登录会关闭这个浏览器打开的内容**，例如终端窗口和网页应用。
- **会作为网页运行的文件**（HTML、SVG、脚本）改为下载，不在浏览器里直接打开。
- **App Store**：没有校验值的软件包不再安装；安装前会再校验一次。管理员权限只在安装那一步使用。
- **Zagent 的页面（`zagent.local`）** 需要先登录 ZeroDesk。

### 你需要做什么

打开 ZeroDesk 重新连接：把浏览器上显示的连接码输入 CardputerZero，再按 OK；也可以改用访问令牌。用 `X-ZeroDesk-Token` 请求头发送访问令牌的脚本不受影响。

局域网里的连接目前仍是 HTTP，局域网 HTTPS 已在计划中。
