import * as React from 'react'
import { AppBar, Logout, UserMenu, AppBarProps } from 'react-admin'

const CustomUserMenu: React.FC = () => (
  <UserMenu>
    <Logout />
  </UserMenu>
)

export const CustomAppBar: React.FC<AppBarProps> = (props) => (
  <AppBar {...props} userMenu={<CustomUserMenu />} />
)

export default CustomAppBar
