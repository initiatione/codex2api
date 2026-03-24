import type { ReactNode } from 'react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { AlertTriangle, ShieldAlert, Trash2 } from 'lucide-react'
import Modal from '../components/Modal'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

type ConfirmTone = 'default' | 'warning' | 'destructive'
type ConfirmVariant = 'default' | 'destructive'

export interface ConfirmDialogOptions {
  title: string
  description: ReactNode
  confirmText?: string
  cancelText?: string
  tone?: ConfirmTone
  confirmVariant?: ConfirmVariant
  icon?: ReactNode
}

interface ResolvedConfirmDialogOptions extends ConfirmDialogOptions {
  confirmText: string
  cancelText: string
  tone: ConfirmTone
  confirmVariant: ConfirmVariant
}

const toneStyles: Record<ConfirmTone, { iconWrap: string; hint: string }> = {
  default: {
    iconWrap: 'border-primary/20 bg-primary/10 text-primary',
    hint: '操作会立即生效，请确认后继续。',
  },
  warning: {
    iconWrap: 'border-amber-500/20 bg-amber-500/10 text-amber-600 dark:text-amber-400',
    hint: '请确认当前选择无误，再继续执行。',
  },
  destructive: {
    iconWrap: 'border-destructive/20 bg-destructive/10 text-destructive',
    hint: '此操作会直接修改当前数据，通常不可撤销。',
  },
}

const toneIcons: Record<ConfirmTone, ReactNode> = {
  default: <ShieldAlert className="size-5" />,
  warning: <AlertTriangle className="size-5" />,
  destructive: <Trash2 className="size-5" />,
}

function ConfirmDialog({
  options,
  onCancel,
  onConfirm,
}: {
  options: ResolvedConfirmDialogOptions
  onCancel: () => void
  onConfirm: () => void
}) {
  const toneStyle = toneStyles[options.tone]

  return (
    <Modal
      show={true}
      title={options.title}
      onClose={onCancel}
      showCloseButton={false}
      contentClassName="sm:max-w-[500px]"
      bodyClassName="px-6 py-5"
      footer={
        <>
          <Button variant="outline" onClick={onCancel} className="min-w-[96px]">
            {options.cancelText}
          </Button>
          <Button variant={options.confirmVariant} onClick={onConfirm} className="min-w-[120px]">
            {options.confirmText}
          </Button>
        </>
      }
    >
      <div className="space-y-5">
        <div className="flex items-start gap-4">
          <div
            className={cn(
              'mt-0.5 flex size-12 shrink-0 items-center justify-center rounded-2xl border shadow-xs',
              toneStyle.iconWrap
            )}
          >
            {options.icon ?? toneIcons[options.tone]}
          </div>
          <div className="min-w-0 space-y-3">
            <div className="text-[15px] leading-7 text-foreground/90">
              {options.description}
            </div>
            <div className="inline-flex rounded-full bg-muted px-3 py-1 text-[12px] font-medium text-muted-foreground">
              {toneStyle.hint}
            </div>
          </div>
        </div>
      </div>
    </Modal>
  )
}

export function useConfirmDialog() {
  const [options, setOptions] = useState<ResolvedConfirmDialogOptions | null>(null)
  const resolverRef = useRef<((value: boolean) => void) | null>(null)

  const closeDialog = useCallback((confirmed: boolean) => {
    const resolve = resolverRef.current
    resolverRef.current = null
    setOptions(null)
    resolve?.(confirmed)
  }, [])

  const confirm = useCallback((nextOptions: ConfirmDialogOptions) => {
    if (resolverRef.current) {
      resolverRef.current(false)
      resolverRef.current = null
    }

    const resolvedOptions: ResolvedConfirmDialogOptions = {
      confirmText: '确认',
      cancelText: '取消',
      tone: 'warning',
      confirmVariant: 'default',
      ...nextOptions,
    }

    return new Promise<boolean>((resolve) => {
      resolverRef.current = resolve
      setOptions(resolvedOptions)
    })
  }, [])

  useEffect(() => {
    return () => {
      if (resolverRef.current) {
        resolverRef.current(false)
        resolverRef.current = null
      }
    }
  }, [])

  return {
    confirm,
    confirmDialog: options ? (
      <ConfirmDialog
        options={options}
        onCancel={() => closeDialog(false)}
        onConfirm={() => closeDialog(true)}
      />
    ) : null,
  }
}
