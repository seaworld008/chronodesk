import * as React from 'react'
import {
  AppBar,
  Logout,
  UserMenu,
  AppBarProps,
  useNotify,
} from 'react-admin'
import { useNavigate } from 'react-router-dom'
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
import { projectScopeChangedEvent } from '@/lib/projectScopeEvents'
import { logoutAllSessions } from '@/lib/authProvider'
import { visibleNavigationItems } from '@/navigation/navigationRegistry'

const LogoutAllMenuItem: React.FC = () => {
  const notify = useNotify()
  const handleLogoutAll = async () => {
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
  security: <SecurityIcon fontSize="small" />,
  loginHistory: <HistoryIcon fontSize="small" />,
}

const AccountNavigationItems: React.FC = () => {
  const navigate = useNavigate()
  const items = visibleNavigationItems('account', {
    platformRole: null,
    projectRole: null,
    hasProject: false,
  })
  return items.map((item) => (
    <MenuItem
      key={item.id}
      onClick={() => navigate(item.path)}
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
  <Box data-testid="account-menu">
    <UserMenu label="账号">
      <AccountNavigationItems />
      <LogoutAllMenuItem />
      <Logout data-testid="logout-current-session" />
    </UserMenu>
  </Box>
)

const ProjectSwitcher: React.FC = () => {
  const navigate = useNavigate()
  const [projects, setProjects] = React.useState<AuthorizedProject[]>([])
  const [selected, setSelected] = React.useState(activeProjectKey() ?? '')
  const [loading, setLoading] = React.useState(true)

  React.useEffect(() => {
    let active = true
    const loadProjects = () => {
      setLoading(true)
      void loadAuthorizedProjects(true)
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
    loadProjects()
    window.addEventListener(projectAccessInvalidatedEvent, loadProjects)
    window.addEventListener(projectScopeChangedEvent, loadProjects)
    return () => {
      active = false
      window.removeEventListener(projectAccessInvalidatedEvent, loadProjects)
      window.removeEventListener(projectScopeChangedEvent, loadProjects)
    }
  }, [])

  if (loading) {
    return (
      <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}>
        <Chip
          label="平台级"
          size="small"
          color="default"
          data-testid="scope-badge"
        />
        <CircularProgress
          color="inherit"
          size={20}
          aria-label="正在加载项目"
          data-testid="project-switcher-loading"
        />
      </Stack>
    )
  }
  if (projects.length === 0) {
    return (
      <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}>
        <Chip
          label="平台级"
          size="small"
          color="default"
          data-testid="scope-badge"
        />
        <Typography
          variant="body2"
          data-testid="no-project-switcher"
          sx={{ display: { xs: 'none', sm: 'block' } }}
        >
          暂无授权项目
        </Typography>
      </Stack>
    )
  }

  const selectedProject = projects.find(
    ({ project }) => project.key === selected,
  )

  return (
    <Stack
      direction="row"
      spacing={1}
      sx={{ alignItems: 'center', minWidth: 0 }}
    >
      <Chip
        label={
          selectedProject
            ? `当前项目：${selectedProject.project.name}`
            : '平台级'
        }
        size="small"
        color={selectedProject ? 'primary' : 'default'}
        data-testid="scope-badge"
        sx={{
          maxWidth: { xs: 96, sm: 240 },
          bgcolor: 'rgba(255, 255, 255, 0.92)',
          color: 'primary.dark',
          '& .MuiChip-label': {
            overflow: 'hidden',
            textOverflow: 'ellipsis',
          },
        }}
      />
      <Box sx={{ minWidth: { xs: 96, sm: 220 } }}>
      <Select
        aria-label="当前项目"
        data-testid="active-project-switcher"
        value={selected}
        size="small"
        onChange={(event) => {
          const projectKey = event.target.value
          setActiveProjectKey(projectKey, projects)
          setSelected(projectKey)
          navigate('/')
        }}
        sx={{
          width: '100%',
          color: 'inherit',
          '& .MuiOutlinedInput-notchedOutline': {
            borderColor: 'rgba(255,255,255,0.45)',
          },
          '& .MuiSvgIcon-root': { color: 'inherit' },
        }}
      >
        <MenuItem value="" disabled>
          请选择项目
        </MenuItem>
        {projects.map(({ project, project_role }) => (
          <MenuItem key={project.public_id} value={project.key}>
            {project.name} · {getProjectRoleLabel(project_role)}
          </MenuItem>
        ))}
      </Select>
      </Box>
    </Stack>
  )
}

export const CustomAppBar: React.FC<AppBarProps> = (props) => (
  <AppBar {...props} userMenu={<CustomUserMenu />}>
    <ProjectSwitcher />
  </AppBar>
)
