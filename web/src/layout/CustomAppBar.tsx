import * as React from 'react'
import { AppBar, Logout, UserMenu, AppBarProps } from 'react-admin'
import {
  Box,
  CircularProgress,
  MenuItem,
  Select,
  Typography,
} from '@mui/material'
import {
  activeProjectKey,
  loadAuthorizedProjects,
  setActiveProjectKey,
  type AuthorizedProject,
} from '@/lib/projectScope'

const CustomUserMenu: React.FC = () => (
  <UserMenu>
    <Logout />
  </UserMenu>
)

const ProjectSwitcher: React.FC = () => {
  const [projects, setProjects] = React.useState<AuthorizedProject[]>([])
  const [selected, setSelected] = React.useState(activeProjectKey() ?? '')
  const [loading, setLoading] = React.useState(true)

  React.useEffect(() => {
    let active = true
    void loadAuthorizedProjects(true)
      .then((authorized) => {
        if (!active) return
        setProjects(authorized)
        const resolved =
          authorized.find(({ project }) => project.key === activeProjectKey()) ??
          authorized[0]
        if (resolved) {
          setActiveProjectKey(resolved.project.key, authorized)
          setSelected(resolved.project.key)
        }
      })
      .finally(() => {
        if (active) setLoading(false)
      })
    return () => {
      active = false
    }
  }, [])

  if (loading) {
    return <CircularProgress color="inherit" size={20} aria-label="正在加载项目" />
  }
  if (projects.length === 0) {
    return <Typography variant="body2">无授权项目</Typography>
  }

  return (
    <Box sx={{ minWidth: 220 }}>
      <Select
        aria-label="当前项目"
        value={selected}
        size="small"
        onChange={(event) => {
          const projectKey = event.target.value
          setActiveProjectKey(projectKey, projects)
          setSelected(projectKey)
          window.location.assign('/')
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
        {projects.map(({ project, role }) => (
          <MenuItem key={project.public_id} value={project.key}>
            {project.name} · {role}
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
