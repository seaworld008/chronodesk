import { Autocomplete, Chip, TextField } from '@mui/material'
import { InputHelperText, useInput } from 'react-admin'
import {
    MAX_TICKET_TAGS,
    normalizeTagList,
    validateTagsInput,
} from './tagUtils'

interface TagChipInputProps {
    source: string
    label?: string
    helperText?: string
    disabled?: boolean
    fullWidth?: boolean
}

const TagChipInput = ({
    source,
    label = '标签',
    helperText = '输入标签后按回车；不区分大小写去重',
    disabled = false,
    fullWidth = true,
}: TagChipInputProps) => {
    const {
        id,
        field,
        fieldState: { error, invalid },
    } = useInput({
        source,
        validate: validateTagsInput,
    })
    const tags = normalizeTagList(field.value)

    return (
        <Autocomplete
            id={id}
            multiple
            freeSolo
            clearOnBlur
            filterSelectedOptions
            disabled={disabled}
            fullWidth={fullWidth}
            options={[] as string[]}
            value={tags}
            onBlur={field.onBlur}
            onChange={(_, nextTags) => field.onChange(normalizeTagList(nextTags))}
            clearText="清空全部标签"
            openText="打开标签选项"
            closeText="关闭标签选项"
            noOptionsText="输入新标签后按回车"
            renderValue={(values, getItemProps) =>
                values.map((tag, index) => {
                    const { key, ...itemProps } = getItemProps({ index })
                    return (
                        <Chip
                            key={key}
                            label={tag}
                            size="small"
                            {...itemProps}
                        />
                    )
                })
            }
            renderInput={(params) => (
                <TextField
                    {...params}
                    name={field.name}
                    label={label}
                    error={invalid}
                    placeholder={
                        tags.length >= MAX_TICKET_TAGS
                            ? '已达到 20 个标签上限'
                            : '输入标签并按回车'
                    }
                    helperText={
                        <InputHelperText
                            error={error?.message}
                            helperText={`${helperText}（${tags.length}/${MAX_TICKET_TAGS}）`}
                        />
                    }
                    slotProps={{
                        ...params.slotProps,
                        htmlInput: {
                            ...params.slotProps.htmlInput,
                            'aria-describedby': `${id}-helper-text`,
                        },
                        formHelperText: {
                            id: `${id}-helper-text`,
                        },
                    }}
                />
            )}
        />
    )
}

export default TagChipInput
