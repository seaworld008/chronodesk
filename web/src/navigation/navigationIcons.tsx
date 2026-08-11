import type { NavigationIcon } from './navigationRegistry'
import { navigationIconComponents } from './navigationIconCatalog'

export interface NavigationIconGlyphProps {
    icon: NavigationIcon
}

export const NavigationIconGlyph = ({ icon }: NavigationIconGlyphProps) => {
    const Icon = navigationIconComponents[icon]
    return (
        <span
            aria-hidden="true"
            data-navigation-icon={icon}
            style={{
                alignItems: 'center',
                display: 'inline-flex',
                height: 24,
                justifyContent: 'center',
                lineHeight: 0,
                width: 24,
            }}
        >
            <Icon fontSize="small" />
        </span>
    )
}
