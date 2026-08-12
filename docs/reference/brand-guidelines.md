# ChronoDesk 品牌标识规范

ChronoDesk 的核心标识名为 **Handoff Core / 交接核**。一个稳定的横向工作核心被
单条负空间交接缝分为左右两个工作面：左侧代表 Human，右侧代表 AI Agent；交接缝
只发生一次柔和位移，表达同一项工作随时间推进、在受控边界内完成可审计交接。

标识刻意不使用机器人、AI 星芒、时钟、工单票券、聊天气泡或勾选框。这些元素在
企业工单与 AI 软件中高度同质化，也难以在小尺寸和单色环境中长期保持辨识度。

## 母版

- 几何基准：`24 × 24`。
- 外轮廓：`x=2, y=4, width=20, height=16`，圆角由左右工作面共同形成。
- 标准交接缝宽：`1.8` 基准单位；顶部中心线 `x=12`，中段柔和右移至
  `x=13.9`，再垂直至底部。
- 数字最小尺寸：`16px`；印刷最小高度：`5mm`。
- 四周安全区：不小于标志高度的 `0.25H`。
- 不旋转、不拉伸、不改变交接缝方向、不增加第三个图形或装饰。

权威可编辑源文件为：

- `brand/source/chronodesk-mark-master.svg`
- `brand/source/chronodesk-mark-optical-16-32.svg`（仅用于 16–32px）

可直接使用的单色导出为：

- `brand/exports/chronodesk-mark-blue.svg`
- `brand/exports/chronodesk-mark-ink.svg`
- `brand/exports/chronodesk-mark-white.svg`

## 颜色

| 名称 | 色值 | 用途 |
| --- | --- | --- |
| Chrono Blue | `#2563EB` | 主品牌色、链接与主操作 |
| Enterprise Ink | `#0F172A` | 单色标识、正文与正式印刷 |
| Midnight | `#0B1726` | 深色品牌背景与 App 图标 |
| Frost | `#F8FAFC` | 浅色背景与反白标识 |

母符号必须先通过纯色测试。渐变、阴影和材质只可出现在 App 图标背景或营销画面，
不得成为识别标志本身的一部分。

## Web 与 App 图标

`brand/app-icon/chronodesk-app-icon.svg` 是 `1024 × 1024`、未预裁切圆角的 App
图标母版。`web/public/` 提供 SVG favicon、16/32px favicon、180px Apple Touch
Icon、192/512px PWA 图标和独立的 maskable 512px 图标。App 图标使用深色完整背景
与反白 Handoff Core，图形安全边距不少于画布的 20%。系统圆角或平台材质由操作系统
处理，不在母版中模拟 Apple 标志、玻璃高光或第三方 App 图标。

16–32px favicon 使用独立光学母版：保持标准母版的 `20:16` 外轮廓比例与圆角，
只把负空间交接缝从 `1.8` 加宽到 `2` 个基准单位，并放入深色 favicon 容器；不得
横向拉伸标志或改变交接方向。

## 字标与锁定关系

`ChronoDesk` 字标与图形保持独立。界面中优先使用系统无衬线字体并保留原始英文大小写；
标志位于字标左侧，二者之间距约为图标宽度的 `0.35–0.45`。不得让图形替代正文中的
单词，也不得由图像模型生成包含文字的品牌母版。

## 商标检查

视觉碰撞审查已主动避开当前 Jira、Linear、Zendesk、Intercom、ServiceNow、
Chronosphere、Temporal、Todoist、Things 与 Home Assistant 的主要构图语言。
这不等同于法律商标清查；对外大规模推广或申请商标前，还需在 WIPO Global Brand
Database 及目标市场官方数据库执行名称和图形检索。
