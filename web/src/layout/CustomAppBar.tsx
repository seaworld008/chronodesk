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
  CircularProgress,
  MenuItem,
  Select,
  Typography,
} from '@mui/material'
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

const CustomUserMenu: React.FC = () => (
  <Box data-testid="account-menu">
    <UserMenu label="账号">
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
      <CircularProgress
        color="inherit"
        size={20}
        aria-label="正在加载项目"
        data-testid="project-switcher-loading"
      />
    )
  }
  if (projects.length === 0) {
    return (
      <Typography variant="body2" data-testid="no-project-switcher">
        暂无授权项目
      </Typography>
    )
  }

  return (
    <Box sx={{ minWidth: 220 }}>
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
          minWidth: 220,
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
  )
}

export const CustomAppBar: React.FC<AppBarProps> = (props) => (
  <AppBar {...props} userMenu={<CustomUserMenu />}>
    <ProjectSwitcher />
  </AppBar>
)
