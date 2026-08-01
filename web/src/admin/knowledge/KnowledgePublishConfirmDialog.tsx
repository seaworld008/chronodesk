import {
    Alert,
    Button,
    Dialog,
    DialogActions,
    DialogContent,
    DialogTitle,
    Stack,
    Typography,
} from '@mui/material'

export const KnowledgePublishConfirmDialog = ({
    open,
    title,
    busy,
    onCancel,
    onConfirm,
}: {
    open: boolean
    title: string
    busy: boolean
    onCancel: () => void
    onConfirm: () => void
}) => (
    <Dialog
        open={open}
        onClose={() => {
            if (!busy) onCancel()
        }}
        fullWidth
        maxWidth="sm"
        aria-labelledby="knowledge-publish-confirm-title"
    >
        <DialogTitle id="knowledge-publish-confirm-title">
            确认发布知识
        </DialogTitle>
        <DialogContent>
            <Stack spacing={2}>
                <Typography>
                    即将发布《{title || '未命名知识'}》。
                </Typography>
                <Alert severity="warning">
                    发布后将对当前项目所有成员可见，并进入人工与 AI Agent
                    的知识搜索。发布版本不可直接修改；后续调整需要创建新草稿。
                </Alert>
            </Stack>
        </DialogContent>
        <DialogActions>
            <Button disabled={busy} onClick={onCancel} autoFocus>
                取消
            </Button>
            <Button
                variant="contained"
                disabled={busy}
                onClick={onConfirm}
            >
                {busy ? '发布中…' : '确认发布'}
            </Button>
        </DialogActions>
    </Dialog>
)
