import React from 'react';

/**
 * 测试应用组件 - 用于调试空白页面问题
 */
const TestApp: React.FC = () => {
    return (
        <div style={{ 
            padding: '20px', 
            fontFamily: 'Arial, sans-serif',
            background: '#f0f0f0',
            minHeight: '100vh'
        }}>
            <h1>🧪 React应用测试页面</h1>
            <p>如果您看到这个页面，说明React应用正常工作！</p>
            <p>当前时间: {new Date().toLocaleString('zh-CN')}</p>
            
            <div style={{ 
                background: 'white', 
                padding: '20px', 
                marginTop: '20px',
                borderRadius: '8px',
                boxShadow: '0 2px 4px rgba(0,0,0,0.1)'
            }}>
                <h2>✅ 系统状态检查</h2>
                <ul>
                    <li>✅ HTML页面加载正常</li>
                    <li>✅ Vite开发服务器运行正常</li>
                    <li>✅ React组件渲染正常</li>
                    <li>✅ JavaScript执行正常</li>
                </ul>
            </div>

            <div style={{ 
                background: '#e3f2fd', 
                padding: '15px', 
                marginTop: '20px',
                borderRadius: '8px',
                border: '1px solid #2196f3'
            }}>
                <p><strong>下一步:</strong> 如果这个测试页面正常显示，我们可以逐步恢复完整的系统设置功能。</p>
            </div>
        </div>
    );
};

export default TestApp;