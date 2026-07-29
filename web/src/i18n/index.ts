import { zhCN as muiZhCN } from '@mui/material/locale'
import polyglotI18nProvider from 'ra-i18n-polyglot'

import zhCNMessages from './zhCN'

export const defaultLocale = 'zh-CN'

/**
 * 自定义页面中仍有少量直接传给 useNotify 的中文句子，因此允许缺失键原样显示；
 * React Admin 自身使用的标准键已经由 zhCNMessages 完整覆盖。
 */
export const i18nProvider = polyglotI18nProvider(
  () => zhCNMessages,
  defaultLocale,
  [{ locale: defaultLocale, name: '简体中文' }],
  { allowMissing: true },
)

export { muiZhCN, zhCNMessages }
