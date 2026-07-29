import React, { memo } from 'react'
import CloseIcon from '@mui/icons-material/Close'
import SearchIcon from '@mui/icons-material/Search'
import {
  IconButton,
  InputAdornment,
  TextField,
  type SxProps,
  type Theme,
} from '@mui/material'
import { FilterLiveForm, useInput, useTranslate } from 'react-admin'

export interface EnterpriseSearchInputProps {
  source: string
  alwaysOn?: boolean
  className?: string
  defaultValue?: unknown
  disabled?: boolean
  fullWidth?: boolean
  helperText?: React.ReactNode | false
  hiddenLabel?: boolean
  label?: React.ReactNode
  placeholder?: string
  readOnly?: boolean
  record?: unknown
  resource?: string
  size?: 'small' | 'medium'
  sx?: SxProps<Theme>
}

/**
 * 使用 Material UI slotProps 的搜索输入。它保留 React Admin 筛选表单
 * 需要的 useInput 契约，并避免将组件专属属性传到原生 DOM。
 */
export const EnterpriseSearchInput = ({
  source,
  className,
  defaultValue,
  disabled,
  fullWidth,
  hiddenLabel = true,
  label,
  placeholder,
  readOnly,
  resource,
  size = 'small',
  sx,
}: EnterpriseSearchInputProps) => {
  const translate = useTranslate()
  const resolvedPlaceholder = placeholder ?? translate('ra.action.search')
  const {
    field,
    fieldState: { invalid },
    id,
  } = useInput({
    source,
    resource,
    defaultValue,
    disabled,
    readOnly,
  })

  const hasValue =
    field.value !== null &&
    field.value !== undefined &&
    String(field.value) !== ''

  return (
    <TextField
      {...field}
      id={id}
      inputRef={field.ref}
      value={field.value ?? ''}
      className={['ra-input', `ra-input-${source}`, className].filter(Boolean).join(' ')}
      label={hiddenLabel ? undefined : label ?? resolvedPlaceholder}
      placeholder={resolvedPlaceholder}
      error={invalid}
      disabled={disabled}
      fullWidth={fullWidth}
      hiddenLabel={hiddenLabel}
      margin="dense"
      size={size}
      sx={sx}
      slotProps={{
        htmlInput: {
          'aria-label': typeof label === 'string' ? label : resolvedPlaceholder,
          readOnly,
        },
        input: {
          startAdornment: (
            <InputAdornment position="start">
              <SearchIcon color="disabled" fontSize="small" />
            </InputAdornment>
          ),
          endAdornment: hasValue ? (
            <InputAdornment position="end">
              <IconButton
                aria-label="清除搜索内容"
                edge="end"
                onClick={() => field.onChange('')}
                size="small"
                title="清除搜索内容"
              >
                <CloseIcon fontSize="small" />
              </IconButton>
            </InputAdornment>
          ) : undefined,
        },
      }}
    />
  )
}

export interface EnterpriseFilterLiveSearchProps
  extends Omit<EnterpriseSearchInputProps, 'source' | 'alwaysOn'> {
  source?: string
}

export const EnterpriseFilterLiveSearch = memo(({
  source = 'q',
  label,
  placeholder,
  hiddenLabel = false,
  ...props
}: EnterpriseFilterLiveSearchProps) => {
  const translate = useTranslate()
  const resolvedLabel = label ?? translate('ra.action.search')

  return (
    <FilterLiveForm>
      <EnterpriseSearchInput
        {...props}
        source={source}
        label={resolvedLabel}
        hiddenLabel={hiddenLabel}
        placeholder={placeholder ?? (hiddenLabel ? String(resolvedLabel) : undefined)}
      />
    </FilterLiveForm>
  )
})

EnterpriseFilterLiveSearch.displayName = 'EnterpriseFilterLiveSearch'
