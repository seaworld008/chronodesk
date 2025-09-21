import React from 'react';
import { Button } from '@mui/material';
import ArrowBackIcon from '@mui/icons-material/ArrowBack';
import { useNavigate } from 'react-router-dom';

interface BackButtonProps {
    label?: string;
    fallbackPath?: string;
}

const BackButton: React.FC<BackButtonProps> = ({ label = '返回上一级', fallbackPath = '/' }) => {
    const navigate = useNavigate();

    const handleClick = () => {
        if (window.history.length > 1) {
            navigate(-1);
        } else {
            navigate(fallbackPath);
        }
    };

    return (
        <Button
            onClick={handleClick}
            startIcon={<ArrowBackIcon />}
            sx={{ mb: 2 }}
            variant="text"
            color="primary"
        >
            {label}
        </Button>
    );
};

export default BackButton;
