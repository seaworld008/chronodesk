import { useCallback, useEffect, useState } from 'react'
import { Button, Snackbar, type SnackbarProps } from '@mui/material'
import {
    CloseNotificationContext,
    undoableEventEmitter,
    useNotificationContext,
    useTakeUndoableMutation,
    useTranslate,
} from 'react-admin'

type NotificationItem = ReturnType<
    typeof useNotificationContext
>['notifications'][number]

const notificationColors = {
    error: {
        backgroundColor: 'error.main',
        color: 'error.contrastText',
    },
    success: {
        backgroundColor: 'success.main',
        color: 'success.contrastText',
    },
    warning: {
        backgroundColor: 'warning.main',
        color: 'warning.contrastText',
    },
} as const

/**
 * React Admin 5.15 still forwards the removed MUI Snackbar
 * `TransitionProps` and `ContentProps` properties. MUI 9 sends those unknown
 * properties to the DOM, which creates console errors. Keep the React Admin
 * notification queue and undo semantics while using the MUI 9 slot API only.
 */
export const AppNotification = () => {
    const { notifications, takeNotification } = useNotificationContext()
    const takeMutation = useTakeUndoableMutation()
    const translate = useTranslate()
    const [open, setOpen] = useState(false)
    const [currentNotification, setCurrentNotification] =
        useState<NotificationItem>()

    useEffect(() => {
        if (notifications.length > 0 && !currentNotification) {
            const notification = takeNotification()
            if (notification) {
                setCurrentNotification(notification)
                setOpen(true)
            }
        }

        if (!currentNotification?.notificationOptions?.undoable) {
            return
        }

        const preventUnload = (event: BeforeUnloadEvent) => {
            event.preventDefault()
            event.returnValue = ''
        }

        window.addEventListener('beforeunload', preventUnload)
        return () => window.removeEventListener('beforeunload', preventUnload)
    }, [currentNotification, notifications, takeNotification])

    const handleClose = useCallback(() => {
        setOpen(false)
    }, [])

    const handleExited = useCallback(() => {
        if (currentNotification?.notificationOptions?.undoable) {
            const mutation = takeMutation()
            if (mutation) {
                mutation({ isUndo: false })
            } else {
                undoableEventEmitter.emit('end', { isUndo: false })
            }
        }
        setCurrentNotification(undefined)
    }, [currentNotification, takeMutation])

    const handleUndo = useCallback(() => {
        const mutation = takeMutation()
        if (mutation) {
            mutation({ isUndo: true })
        } else {
            undoableEventEmitter.emit('end', { isUndo: true })
        }
        setOpen(false)
    }, [takeMutation])

    if (!currentNotification) {
        return null
    }

    const {
        message,
        notificationOptions,
        type = 'info',
    } = currentNotification
    const {
        autoHideDuration = 4000,
        messageArgs,
        multiLine = false,
        undoable = false,
        // Never forward the removed MUI properties if a caller supplies them.
        ContentProps: legacyContentProps,
        TransitionProps: legacyTransitionProps,
        slotProps: notificationSlotProps,
        ...notificationSnackbarProps
    } = notificationOptions ?? {}

    const colorSx =
        type === 'info' ? undefined : notificationColors[type]
    const contentSlotProps = notificationSlotProps?.content
    const transitionSlotProps = notificationSlotProps?.transition

    return (
        <CloseNotificationContext.Provider value={handleClose}>
            <Snackbar
                {...(notificationSnackbarProps as Omit<
                    SnackbarProps,
                    'open' | 'slotProps'
                >)}
                open={open}
                message={
                    typeof message === 'string'
                        ? translate(message, messageArgs)
                        : <div>{message}</div>
                }
                autoHideDuration={autoHideDuration ?? undefined}
                disableWindowBlurListener={undoable}
                onClose={handleClose}
                anchorOrigin={{ vertical: 'bottom', horizontal: 'center' }}
                action={
                    undoable ? (
                        <Button
                            color="primary"
                            size="small"
                            onClick={handleUndo}
                        >
                            {translate('ra.action.undo')}
                        </Button>
                    ) : null
                }
                slotProps={{
                    ...notificationSlotProps,
                    content: {
                        ...legacyContentProps,
                        ...contentSlotProps,
                        sx: {
                            ...colorSx,
                            ...(multiLine ? { whiteSpace: 'pre-wrap' } : {}),
                            ...contentSlotProps?.sx,
                        },
                    },
                    transition: {
                        ...legacyTransitionProps,
                        ...transitionSlotProps,
                        onExited: handleExited,
                    },
                }}
            />
        </CloseNotificationContext.Provider>
    )
}
