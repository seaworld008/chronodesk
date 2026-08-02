import { useState } from 'react'
import {
    useGetIdentity,
    useListContext,
    useNotify,
    useRecordContext,
} from 'react-admin'
import {
    Button,
    Dialog,
    DialogActions,
    DialogContent,
    DialogContentText,
    DialogTitle,
    IconButton,
    Stack,
    Tooltip,
} from '@mui/material'
import {
    DoneAll as MarkAllReadIcon,
    MarkEmailRead as MarkReadIcon,
    DeleteOutlined as DeleteIcon,
} from '@mui/icons-material'
import {
    humanApiRoutes,
    type Notification,
} from '@/lib/generated/human-api'
import {
    apiFetch,
    localizedUnknownErrorMessage,
} from '@/lib/apiClient'
import { resolveActiveProjectKey } from '@/lib/projectScope'

type NotificationActionRecord = Notification & {
    recipient_id?: number
}

const notificationActionPath = async (
    operation: 'read' | 'delete',
    notificationID: number,
) => {
    const projectKey = await resolveActiveProjectKey()
    return operation === 'read'
        ? humanApiRoutes.markProjectNotificationRead({
              projectKey,
              notificationID,
          })
        : humanApiRoutes.deleteProjectNotification({
              projectKey,
              notificationID,
          })
}

export const MarkAllNotificationsReadButton = () => {
    const notify = useNotify()
    const { refetch } = useListContext()
    const [submitting, setSubmitting] = useState(false)

    const markAllRead = async () => {
        setSubmitting(true)
        try {
            const projectKey = await resolveActiveProjectKey()
            await apiFetch<void>(
                humanApiRoutes.markAllProjectNotificationsRead({
                    projectKey,
                }),
                { method: 'PUT' },
            )
            await refetch()
            notify('全部通知已标为已读', { type: 'success' })
        } catch (error: unknown) {
            notify(
                localizedUnknownErrorMessage(
                    error,
                    '全部标为已读失败，请稍后重试',
                ),
                { type: 'error' },
            )
        } finally {
            setSubmitting(false)
        }
    }

    return (
        <Button
            aria-label="全部标为已读"
            disabled={submitting}
            onClick={() => void markAllRead()}
            startIcon={<MarkAllReadIcon />}
        >
            {submitting ? '正在标记…' : '全部标为已读'}
        </Button>
    )
}

export const NotificationRowActions = ({
    canDelete,
}: {
    canDelete: boolean
}) => {
    const record = useRecordContext<NotificationActionRecord>()
    const notify = useNotify()
    const { refetch } = useListContext()
    const { identity } = useGetIdentity()
    const [markingRead, setMarkingRead] = useState(false)
    const [deleteDialogOpen, setDeleteDialogOpen] = useState(false)
    const [deleting, setDeleting] = useState(false)

    if (!record) return null
    const identityID = Number(identity?.id)
    const recipientID = record.recipient_id ?? record.recipient?.id
    const canMarkRead =
        !record.is_read &&
        Number.isSafeInteger(identityID) &&
        recipientID === identityID

    const markRead = async () => {
        setMarkingRead(true)
        try {
            await apiFetch<void>(
                await notificationActionPath('read', record.id),
                { method: 'PUT' },
            )
            await refetch()
            notify('通知已标为已读', { type: 'success' })
        } catch (error: unknown) {
            notify(
                localizedUnknownErrorMessage(
                    error,
                    '标为已读失败，请稍后重试',
                ),
                { type: 'error' },
            )
        } finally {
            setMarkingRead(false)
        }
    }

    const deleteNotification = async () => {
        setDeleting(true)
        try {
            await apiFetch<void>(
                await notificationActionPath('delete', record.id),
                { method: 'DELETE' },
            )
            setDeleteDialogOpen(false)
            await refetch()
            notify('通知已删除', { type: 'success' })
        } catch (error: unknown) {
            notify(
                localizedUnknownErrorMessage(
                    error,
                    '删除通知失败，请稍后重试',
                ),
                { type: 'error' },
            )
        } finally {
            setDeleting(false)
        }
    }

    return (
        <>
            <Stack direction="row" spacing={0.5}>
                {canMarkRead && (
                    <Tooltip title="标为已读">
                        <span>
                            <IconButton
                                aria-label={`将“${record.title}”标为已读`}
                                disabled={markingRead}
                                onClick={(event) => {
                                    event.stopPropagation()
                                    void markRead()
                                }}
                                size="small"
                            >
                                <MarkReadIcon fontSize="small" />
                            </IconButton>
                        </span>
                    </Tooltip>
                )}
                {canDelete && (
                    <Tooltip title="删除通知">
                        <span>
                            <IconButton
                                aria-label={`删除通知“${record.title}”`}
                                color="error"
                                disabled={deleting}
                                onClick={(event) => {
                                    event.stopPropagation()
                                    setDeleteDialogOpen(true)
                                }}
                                size="small"
                            >
                                <DeleteIcon fontSize="small" />
                            </IconButton>
                        </span>
                    </Tooltip>
                )}
            </Stack>

            <Dialog
                aria-describedby={`delete-notification-description-${record.id}`}
                aria-labelledby={`delete-notification-title-${record.id}`}
                open={deleteDialogOpen}
                onClose={() => {
                    if (!deleting) setDeleteDialogOpen(false)
                }}
            >
                <DialogTitle id={`delete-notification-title-${record.id}`}>
                    确认删除通知
                </DialogTitle>
                <DialogContent>
                    <DialogContentText
                        id={`delete-notification-description-${record.id}`}
                    >
                        删除后无法恢复。即将删除“{record.title}”，是否继续？
                    </DialogContentText>
                </DialogContent>
                <DialogActions>
                    <Button
                        disabled={deleting}
                        onClick={() => setDeleteDialogOpen(false)}
                    >
                        取消
                    </Button>
                    <Button
                        color="error"
                        disabled={deleting}
                        onClick={() => void deleteNotification()}
                        variant="contained"
                    >
                        {deleting ? '正在删除…' : '确认删除'}
                    </Button>
                </DialogActions>
            </Dialog>
        </>
    )
}
