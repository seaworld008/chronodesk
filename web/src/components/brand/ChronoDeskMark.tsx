import { Box, type BoxProps } from '@mui/material'

interface ChronoDeskMarkProps extends Omit<BoxProps, 'children'> {
    title?: string
}

const ChronoDeskMark = ({
    title,
    ...props
}: ChronoDeskMarkProps) => (
    <Box
        component="svg"
        viewBox="0 0 24 24"
        role={title ? 'img' : undefined}
        aria-hidden={title ? undefined : true}
        {...props}
    >
        {title ? <title>{title}</title> : null}
        <path
            fill="currentColor"
            fillRule="evenodd"
            clipRule="evenodd"
            d={
                'M7 4h10a5 5 0 0 1 5 5v6a5 5 0 0 1-5 5H7a5 5 0 0 1-5-5V9a5 5 0 0 1 5-5Z' +
                'm5.9 0h-1.8v5.1c0 .8.4 1.5 1.1 1.9l.6.4c.7.4 1.1 1.1 1.1 1.9V20h1.8v-6.7c0-1.4-.7-2.7-1.9-3.4l-.6-.4c-.2-.1-.3-.3-.3-.5V4Z'
            }
        />
    </Box>
)

export default ChronoDeskMark
