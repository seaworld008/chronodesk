import * as React from 'react'
import {
  AppBar,
  Logout,
  TitlePortal,
  UserMenu,
  useNotify,
  useUserMenu,
  type AppBarProps,
} from 'react-admin'
import { useLocation, useNavigate } from 'react-router-dom'
import {
  Box,
  Chip,
  CircularProgress,
  ListItemIcon,
  ListItemText,
  MenuItem,
  Select,
  Stack,
  Typography,
} from '@mui/material'
import {
  History as HistoryIcon,
  Person as PersonIcon,
  Security as SecurityIcon,
} from '@mui/icons-material'
import {
  activeProjectKey,
  clearActiveProjectSelection,
  getProjectRoleLabel,
  loadAuthorizedProjects,
  projectAccessInvalidatedEvent,
  setActiveProjectKey,
  type AuthorizedProject,
} from '@/lib/projectScope'
import {
  projectInventoryChangedEvent,
  projectScopeChangedEvent,
} from '@/lib/projectScopeEvents'
import { logoutAllSessions } from '@/lib/authProvider'
import { visibleNavigationItems } from '@/navigation/navigationRegistry'
import { resolveRoutePageScope } from './routePageScope'

const LogoutAllMenuItem: React.FC = () => {
  const notify = useNotify()
  const onClose = useUserMenu()?.onClose
  const handleLogoutAll = async () => {
    onClose?.()
    try {
      await logoutAllSessions()
      window.location.assign('/login')
    } catch {
      notify('已清理本地登录状态，请重新登录', { type: 'warning' })
      window.location.assign('/login')
    }
  }
  return (
    <MenuItem
      data-testid="logout-all-sessions"
      onClick={() => void handleLogoutAll()}
    >
      从所有设备退出
    </MenuItem>
  )
}

const accountIcons = {
  users: <PersonIcon fontSize="small" />,
  security: <SecurityIcon fontSize="small" />,
  loginHistory: <HistoryIcon fontSize="small" />,
}

const AccountNavigationItems: React.FC = () => {
  const navigate = useNavigate()
  const onClose = useUserMenu()?.onClose
  const items = visibleNavigationItems('account', {
    platformRole: null,
    projectRole: null,
    hasProject: false,
  })
  return items.map((item) => (
    <MenuItem
      key={item.id}
      onClick={() => {
        onClose?.()
        navigate(item.path)
      }}
      data-testid={`account-menu-${item.id}`}
    >
      <ListItemIcon>
        {accountIcons[item.icon as keyof typeof accountIcons]}
      </ListItemIcon>
      <ListItemText>{item.label}</ListItemText>
    </MenuItem>
  ))
}

const CustomUserMenu: React.FC = () => (
  <Box
    data-testid="account-menu"
    sx={{
      flex: '0 1 auto',
      maxWidth: { xs: 44, lg: 192 },
      minWidth: 0,
      overflow: 'hidden',
    }}
  >
    <UserMenu label="账号" className="ChronoDeskUserMenu">
      <AccountNavigationItems />
      <LogoutAllMenuItem />
      <Logout data-testid="logout-current-session" />
    </UserMenu>
  </Box>
)

interface PageScopeBadgeProps {
  pathname: string
  selectedProject?: AuthorizedProject
}

const PageScopeBadge = ({
  pathname,
  selectedProject,
}: PageScopeBadgeProps) => {
  const resolution = resolveRoutePageScope(pathname)
  const visibleLabel = resolution.kind === 'project'
    ? selectedProject
      ? `当前项目：${selectedProject.project.name}`
      : '当前项目未选择'
    : resolution.kind === 'platform'
      ? '平台级'
      : resolution.kind === 'account'
        ? '个人账号'
        : resolution.navigationNodeID?.includes('workbench')
          ? '跨项目'
          : '全局聚合'
  const accessibleLabel = `页面作用域：${visibleLabel}`

  return (
    <Box
      data-testid="page-scope-badge"
      data-page-scope={resolution.kind}
      data-navigation-node={resolution.navigationNodeID ?? ''}
      sx={{ flex: '0 0 auto', minWidth: 0 }}
    >
      <Chip
        aria-label={accessibleLabel}
        data-testid="scope-badge"
        label={visibleLabel}
        size="small"
        title={accessibleLabel}
        sx={{
          bgcolor: 'rgba(255, 255, 255, 0.94)',
          color: 'primary.dark',
          maxWidth: { xs: 96, sm: 220 },
          '& .MuiChip-label': {
            overflow: 'hidden',
            textOverflow: 'ellipsis',
          },
        }}
      />
    </Box>
  )
}

interface WorkProjectSwitcherProps {
  loading: boolean
  projects: AuthorizedProject[]
  selected: string
  onChange: (projectKey: string) => void
}

const WorkProjectSwitcher = ({
  loading,
  projects,
  selected,
  onChange,
}: WorkProjectSwitcherProps) => {
  if (loading) {
    return (
      <Box
        data-testid="project-switcher-loading"
        role="status"
        aria-label="正在加载工作项目"
        sx={{ display: 'grid', flex: '1 1 10rem', minWidth: 44, placeItems: 'center' }}
      >
        <CircularProgress color="inherit" size={20} />
      </Box>
    )
  }
  if (projects.length === 0) {
    return (
      <Typography
        data-testid="no-project-switcher"
        noWrap
        title="暂无授权项目"
        variant="body2"
        sx={{ flex: '1 1 10rem', minWidth: 0 }}
      >
        暂无授权项目
      </Typography>
    )
  }

  const selectedProject = projects.find(
    ({ project }) => project.key === selected,
  )

  return (
    <Box
      data-testid="work-project-control"
      sx={{
        flex: '1 1 13rem',
        maxWidth: 280,
        minWidth: 0,
      }}
    >
      <Select
        aria-label="工作项目选择"
        data-testid="active-project-switcher"
        displayEmpty
        value={selected}
        size="small"
        onChange={(event) => onChange(event.target.value)}
        renderValue={() => selectedProject?.project.name ?? '选择工作项目'}
        sx={{
          width: '100%',
          color: 'inherit',
          '& .MuiOutlinedInput-notchedOutline': {
            borderColor: 'rgba(255,255,255,0.55)',
          },
          '&:hover .MuiOutlinedInput-notchedOutline': {
            borderColor: 'rgba(255,255,255,0.82)',
          },
          '& .MuiSelect-select': {
            minWidth: '0 !important',
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
          },
          '& .MuiSvgIcon-root': { color: 'inherit' },
        }}
      >
        <MenuItem value="" disabled>
          请选择工作项目
        </MenuItem>
        {projects.map(({ project, project_role }) => (
          <MenuItem key={project.public_id} value={project.key}>
            {project.name} · {getProjectRoleLabel(project_role)}
          </MenuItem>
        ))}
      </Select>
    </Box>
  )
}

const AppBarContextControls: React.FC = () => {
  const navigate = useNavigate()
  const { pathname } = useLocation()
  const [projects, setProjects] = React.useState<AuthorizedProject[]>([])
  const [selected, setSelected] = React.useState(activeProjectKey() ?? '')
  const [loading, setLoading] = React.useState(true)

  React.useEffect(() => {
    let active = true
    const loadProjects = (force: boolean) => {
      setLoading(true)
      void loadAuthorizedProjects(force)
        .then((authorized) => {
          if (!active) return
          setProjects(authorized)
          const storedProjectKey = activeProjectKey()
          const resolved = authorized.find(
            ({ project }) => project.key === storedProjectKey,
          )
          if (resolved) {
            setSelected(resolved.project.key)
          } else {
            clearActiveProjectSelection()
            setSelected('')
          }
        })
        .catch(() => {
          if (!active) return
          clearActiveProjectSelection()
          setProjects([])
          setSelected('')
        })
        .finally(() => {
          if (active) setLoading(false)
        })
    }
    const reloadProjects = () => loadProjects(true)
    const loadCachedProjects = () => loadProjects(false)
    reloadProjects()
    window.addEventListener(projectAccessInvalidatedEvent, reloadProjects)
    window.addEventListener(projectInventoryChangedEvent, loadCachedProjects)
    window.addEventListener(projectScopeChangedEvent, reloadProjects)
    return () => {
      active = false
      window.removeEventListener(projectAccessInvalidatedEvent, reloadProjects)
      window.removeEventListener(
        projectInventoryChangedEvent,
        loadCachedProjects,
      )
      window.removeEventListener(projectScopeChangedEvent, reloadProjects)
    }
  }, [])

  const selectedProject = projects.find(
    ({ project }) => project.key === selected,
  )

  return (
    <Stack
      data-testid="appbar-context-controls"
      direction="row"
      spacing={0.75}
      sx={{
        alignItems: 'center',
        justifyContent: 'center',
        minWidth: 0,
        width: '100%',
      }}
    >
      <PageScopeBadge
        pathname={pathname}
        selectedProject={selectedProject}
      />
      <WorkProjectSwitcher
        loading={loading}
        projects={projects}
        selected={selected}
        onChange={(projectKey) => {
          setActiveProjectKey(projectKey, projects)
          setSelected(projectKey)
          navigate('/')
        }}
      />
    </Stack>
  )
}

export const CustomAppBar: React.FC<AppBarProps> = ({
  sx,
  ...props
}) => (
  <AppBar
    {...props}
    userMenu={<CustomUserMenu />}
    sx={[
      {
        '& .RaAppBar-toolbar': {
          boxSizing: 'border-box',
          columnGap: { xs: 0.25, sm: 0.5 },
          flexWrap: 'nowrap',
          maxWidth: '100%',
          minWidth: 0,
          overflow: 'hidden',
        },
        '& .RaAppBar-menuButton, & .RaLoadingIndicator-root': {
          flex: '0 0 auto',
        },
        '& .ChronoDeskUserMenu': {
          maxWidth: 192,
          minWidth: 0,
          overflow: 'hidden',
        },
        '& .ChronoDeskUserMenu .RaUserMenu-userButton': {
          boxSizing: 'border-box',
          maxWidth: '100%',
          minWidth: 44,
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
        },
        '& .ChronoDeskUserMenu .RaUserMenu-userButton .MuiButton-startIcon': {
          flex: '0 0 auto',
        },
        '@media (max-width:1199.95px)': {
          '& .ChronoDeskUserMenu': {
            maxWidth: 44,
            width: 44,
          },
          '& .ChronoDeskUserMenu .RaUserMenu-userButton': {
            fontSize: 0,
            maxWidth: 44,
            minWidth: 44,
            px: 1,
            width: 44,
          },
          '& .ChronoDeskUserMenu .RaUserMenu-userButton .MuiButton-startIcon': {
            m: 0,
          },
        },
      },
      ...(sx ? (Array.isArray(sx) ? sx : [sx]) : []),
    ]}
  >
    <Box
      data-testid="appbar-three-segment-layout"
      sx={{
        alignItems: 'center',
        display: 'grid',
        flex: '1 1 auto',
        gridTemplateColumns: {
          xs: 'minmax(0, 1fr)',
          md: 'minmax(0, 1fr) minmax(240px, min(48vw, 560px)) minmax(0, 1fr)',
        },
        height: '100%',
        minWidth: 0,
      }}
    >
      <TitlePortal
        data-testid="appbar-title-portal"
        sx={{
          display: { xs: 'none', md: 'block' },
          gridColumn: 1,
          minWidth: 0,
          pr: 1,
        }}
      />
      <Box
        sx={{
          gridColumn: { xs: 1, md: 2 },
          minWidth: 0,
        }}
      >
        <AppBarContextControls />
      </Box>
      <Box
        aria-hidden="true"
        sx={{
          display: { xs: 'none', md: 'block' },
          gridColumn: 3,
          minWidth: 0,
        }}
      />
    </Box>
  </AppBar>
)
