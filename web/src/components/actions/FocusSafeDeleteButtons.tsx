import type { MouseEvent, PropsWithChildren } from 'react';
import {
    BulkDeleteWithConfirmButton,
    DeleteButton,
    type BulkDeleteWithConfirmButtonProps,
    type DeleteButtonProps,
} from 'react-admin';

const FocusReleaseBoundary = ({ children }: PropsWithChildren) => {
    const releaseTriggerFocus = (event: MouseEvent<HTMLSpanElement>) => {
        const target = event.target;
        if (target instanceof Element) {
            const trigger = target.closest('button');
            if (trigger instanceof HTMLElement) {
                trigger.blur();
            }
        }
    };

    return (
        <span
            style={{ display: 'contents' }}
            onClickCapture={releaseTriggerFocus}
        >
            {children}
        </span>
    );
};

export const FocusSafeDeleteButton = (props: DeleteButtonProps) => (
    <FocusReleaseBoundary>
        <DeleteButton {...props} />
    </FocusReleaseBoundary>
);

export const FocusSafeBulkDeleteWithConfirmButton = (
    props: BulkDeleteWithConfirmButtonProps,
) => (
    <FocusReleaseBoundary>
        <BulkDeleteWithConfirmButton {...props} />
    </FocusReleaseBoundary>
);
