
# React‑Admin（MUI v5）Dashboard 优化提示词（交给 Codex 直接执行）

> 目标：让 Dashboard 在桌面端铺满内容区、底部两块等宽自适应、“团队关注”等小卡片对齐统一，并提升可访问性与代码质量。**全部动态响应式，不写死宽高**。请将本提示词完整粘贴给 Codex。

---

## 你的角色
你是资深 **React‑Admin + MUI v5** 前端架构师与 UI 规范把关人。请**不改接口/数据结构**前提下重构 Dashboard 布局与样式，使其：
- 右侧内容区 **100% 宽度**、**min-height: calc(100vh - AppBar)**、无水平滚动条与无意义留白；
- 底部 **「运营快照」** 与 **「工单动态」** 在 `lg≥1200px` 等宽 **50/50**，`md` 以下垂直堆叠；
- 中间 **「团队关注」** 等小卡片 **同高对齐**；
- 间距、字体层级、状态标签排版统一；
- 保持良好 Lighthouse / CLS 指标与可访问性。

完成后请输出 **git diff** 与 **简短变更说明**。

---

## Definition of Done（验收标准）
1. **全屏铺满**：右侧内容区宽度=100%；高度 `min-height: calc(100vh - AppBar)`；无水平滚动条与无意义留白。  
2. **等宽自适应**：底部两块在 `lg≥1200px` 时 **50% : 50% 等宽**，在 `md` 以下垂直堆叠（各 100%）。卡片最小高度自适应：`minHeight: { xs: 'auto', md: 420 }`，内部内容区滚动。  
3. **卡片对齐**：上方小卡片（含 **团队关注** / **SLA 与风险** / **状态分布** …）**同高**、标题/数字区/操作区基线对齐，左右外边距一致。  
4. **统一网格与间距**：12 栅格；列间距 16px（`spacing={2}`）；卡片圆角 12–16px，阴影柔和；文本、标签、图标尺寸层级统一。  
5. **无写死值**：使用 MUI 断点与 `flex/grid` 自适应，不写死 px 宽高（除最小高度与圆角等 UI 常量）。  
6. **可访问性**：对比度达 AA；可点击区域 ≥ 40px；键盘焦点可见。  
7. **代码质量**：样式集中化（`sx`/`styled`/CSS Modules 任一），命名语义化；不使用零散内联样式；提供注释和变更摘要。

---

## 实施要求（React‑Admin / MUI 具体做法）

### 1）移除内容区最大宽度限制（关键）
- 检查 `Layout` / `App.tsx` / `Layout.tsx`：若存在 `<Container maxWidth="xl">` 等包裹，改为 `maxWidth={false}` 或移除。  
- 覆盖 RA 默认内容容器宽度限制（类名以 `.RaLayout-` 开头）。新增/修改自定义 Layout：

```tsx
// src/layout/CustomLayout.tsx
import { Layout as RaLayout, LayoutProps } from 'react-admin';

export const Layout = (props: LayoutProps) => (
  <RaLayout
    {...props}
    sx={{
      '& .RaLayout-content, & .RaLayout-contentWithSidebar': {
        maxWidth: 'none'
      },
      // 让内容区至少填满视口高度（扣除 AppBar）
      '& .RaLayout-content': (theme) => ({
        minHeight: `calc(100vh - ${theme.mixins.toolbar.minHeight}px)`,
        display: 'flex',
        flexDirection: 'column',
      }),
    }}
  />
);
```

> 若项目已自定义 `Layout`，直接合入上述 `sx`；否则新建并在 `<Admin layout={Layout} …>` 中启用。

---

### 2）Dashboard 栅格与等宽自适应
- 使用 **MUI Grid**（配合断点）。示例：

```tsx
// src/dashboard/Dashboard.tsx
import { Title } from 'react-admin';
import { Grid, Card, CardContent, Box, useTheme, useMediaQuery } from '@mui/material';

export const Dashboard = () => {
  const theme = useTheme();
  const isMdDown = useMediaQuery(theme.breakpoints.down('md'));

  return (
    <Box sx={{ p: 2 }}>
      <Title title="工单运营总览" />
      <Grid container spacing={2} alignItems="stretch">
        {/* 顶部 KPI（示例 4 个，按需增减） */}
        {[0,1,2,3].map(i => (
          <Grid key={i} item xs={12} md={6} xl={3}>
            <Card sx={{ height: '100%', borderRadius: 2, boxShadow: 3 }}>
              <CardContent sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
                {/* 标题/数值/次要信息 */}
              </CardContent>
            </Card>
          </Grid>
        ))}

        {/* 中间小卡片：团队关注 / SLA 与风险 / 状态分布 …… —— 强制同高 */}
        {['状态分布','SLA 与风险','团队关注','其他'].map((t) => (
          <Grid key={t} item xs={12} md={6} xl={3}>
            <Card sx={{ height: '100%', minHeight: 112, borderRadius: 2, boxShadow: 3 }}>
              <CardContent sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                {/* 确保“团队关注”等与同列卡片视觉一致 */}
              </CardContent>
            </Card>
          </Grid>
        ))}

        {/* 底部两块：运营快照 / 工单动态 —— lg 及以上等宽 50/50，md 及以下堆叠 */}
        <Grid item xs={12} lg={6}>
          <Card sx={{ height: '100%', minHeight: { xs: 'auto', md: 420 }, borderRadius: 2, boxShadow: 3, display: 'flex', flexDirection: 'column' }}>
            <Box sx={{ px: 2, pt: 2, pb: 1, fontWeight: 600 }}>运营快照</Box>
            <Box sx={{ px: 2, pb: 2, overflow: 'auto', flex: 1 }}>
              {/* 列表主体，内部滚动，文本溢出省略 */}
            </Box>
          </Card>
        </Grid>
        <Grid item xs={12} lg={6}>
          <Card sx={{ height: '100%', minHeight: { xs: 'auto', md: 420 }, borderRadius: 2, boxShadow: 3, display: 'flex', flexDirection: 'column' }}>
            <Box sx={{ px: 2, pt: 2, pb: 1, fontWeight: 600 }}>工单动态</Box>
            <Box sx={{ px: 2, pb: 2, overflow: 'auto', flex: 1 }}>
              {/* 列表主体，状态标签右对齐 */}
            </Box>
          </Card>
        </Grid>
      </Grid>
    </Box>
  );
};
```

---

### 3）列表与标签的排版（复用样式）
```tsx
// 供快照/动态列表复用的行样式
export const rowSx = {
  display: 'grid',
  gridTemplateColumns: '1fr auto', // 左文案 右状态
  alignItems: 'center',
  gap: 1,
  py: 1,
  borderBottom: '1px dashed rgba(0,0,0,0.06)',
  '&:last-of-type': { borderBottom: 'none' },
  '& .title': { overflow: 'hidden', whiteSpace: 'nowrap', textOverflow: 'ellipsis' },
  '& .status': { ml: 2, whiteSpace: 'nowrap' },
};
```

---

### 4）主题与样式收敛（可选但推荐）
```ts
// src/theme.ts（如已存在则合并）
export const themeOverrides = {
  shape: { borderRadius: 12 },
  components: {
    MuiCard: { styleOverrides: { root: { overflow: 'hidden' } } },
    MuiChip: { styleOverrides: { root: { fontWeight: 600 } } },
  },
};
// 在 <Admin theme={createTheme(themeOverrides)} … /> 中启用
```

---

## 交付物
1. **代码 diff**（关键文件：`src/layout/CustomLayout.tsx`、`src/dashboard/Dashboard.tsx`、（可选）`src/theme.ts`）。  
2. **变更摘要**：
   - 如何移除内容区 `max-width`；  
   - 栅格断点策略（`xs / md / lg / xl`）；  
   - 底部两卡片等宽与内部滚动实现；  
   - 小卡片同高与“团队关注”对齐方式；  
   - 可访问性与性能小结（CLS / 对比度 / 焦点样式）。  
3. **提交信息建议**：  
   `feat(dashboard): full‑bleed layout, equal‑width panels, aligned mini‑cards, responsive & a11y polish`

> 如发现历史样式/主题覆盖冲突，请一并修复并在摘要中说明原因与处理方式；**严禁写死宽度或高度**（除 `minHeight` 与圆角等 UI 常量）。  
> 另外，请将右上角“今日 / 近7天 / 近30天 / 刷新”做成响应式换行并与标题对齐（`flex-wrap: wrap` + `gap`）。
