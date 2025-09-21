#!/usr/bin/env python3
"""
修复 email_configs 表缺少列的问题

使用Python脚本直接连接数据库并添加缺少的列
"""

import psycopg2
import sys
import os
from datetime import datetime

# 数据库连接配置
DB_CONFIG = {
    'host': 'ep-holy-salad-a5tol9rs.us-east-2.aws.neon.tech',
    'port': 5432,
    'database': 'neondb',
    'user': 'neondb_owner',
    'password': os.getenv('DB_PASSWORD', 'jGNa7CYlrN9E')
}

def connect_to_database():
    """连接到数据库"""
    try:
        conn = psycopg2.connect(**DB_CONFIG)
        print("✅ 数据库连接成功")
        return conn
    except Exception as e:
        print(f"❌ 数据库连接失败: {e}")
        return None

def check_table_schema(conn):
    """检查email_configs表的当前结构"""
    try:
        cursor = conn.cursor()
        
        # 查询表结构
        cursor.execute("""
            SELECT column_name, data_type, is_nullable, column_default
            FROM information_schema.columns 
            WHERE table_name = 'email_configs' 
            ORDER BY ordinal_position;
        """)
        
        columns = cursor.fetchall()
        cursor.close()
        
        print(f"\n📋 email_configs 表当前结构 ({len(columns)} 列):")
        print("-" * 80)
        for col in columns:
            column_name, data_type, is_nullable, default = col
            nullable = "NULL" if is_nullable == "YES" else "NOT NULL"
            default_str = f"DEFAULT {default}" if default else ""
            print(f"  {column_name:<25} {data_type:<15} {nullable:<8} {default_str}")
        print("-" * 80)
        
        return [col[0] for col in columns]
        
    except Exception as e:
        print(f"❌ 检查表结构失败: {e}")
        return []

def add_missing_columns(conn):
    """添加缺少的列"""
    try:
        cursor = conn.cursor()
        
        # 检查当前列
        current_columns = check_table_schema(conn)
        
        # 需要添加的列
        required_columns = [
            ('smtp_use_tls', 'BOOLEAN', 'DEFAULT TRUE'),
            ('smtp_use_ssl', 'BOOLEAN', 'DEFAULT FALSE')
        ]
        
        added_columns = []
        
        for col_name, col_type, col_default in required_columns:
            if col_name not in current_columns:
                sql = f"ALTER TABLE email_configs ADD COLUMN {col_name} {col_type} {col_default};"
                print(f"🔄 添加列: {col_name}")
                print(f"   SQL: {sql}")
                
                cursor.execute(sql)
                added_columns.append(col_name)
                print(f"✅ 成功添加列: {col_name}")
            else:
                print(f"ℹ️  列已存在: {col_name}")
        
        if added_columns:
            conn.commit()
            print(f"\n✅ 成功添加 {len(added_columns)} 个列: {', '.join(added_columns)}")
        else:
            print("\nℹ️  所有必需的列都已存在，无需添加")
        
        cursor.close()
        return True
        
    except Exception as e:
        print(f"❌ 添加列失败: {e}")
        conn.rollback()
        return False

def verify_columns(conn):
    """验证列是否成功添加"""
    try:
        cursor = conn.cursor()
        
        # 检查特定列是否存在
        cursor.execute("""
            SELECT column_name 
            FROM information_schema.columns 
            WHERE table_name = 'email_configs' 
            AND column_name IN ('smtp_use_tls', 'smtp_use_ssl');
        """)
        
        found_columns = [row[0] for row in cursor.fetchall()]
        cursor.close()
        
        print(f"\n🔍 验证结果:")
        for col in ['smtp_use_tls', 'smtp_use_ssl']:
            if col in found_columns:
                print(f"✅ 列 {col} 存在")
            else:
                print(f"❌ 列 {col} 不存在")
        
        return len(found_columns) == 2
        
    except Exception as e:
        print(f"❌ 验证失败: {e}")
        return False

def test_email_config_service(conn):
    """测试邮件配置服务是否正常工作"""
    try:
        cursor = conn.cursor()
        
        # 创建测试配置
        test_config = {
            'email_verification_enabled': False,
            'smtp_host': 'smtp.gmail.com',
            'smtp_port': 587,
            'smtp_username': 'test@example.com',
            'smtp_password': 'test-password',
            'smtp_use_tls': True,
            'smtp_use_ssl': False,
            'from_email': 'noreply@ticketsystem.com',
            'from_name': '工单系统测试',
            'welcome_email_subject': '欢迎使用工单系统',
            'otp_email_subject': '邮箱验证码',
            'is_active': True
        }
        
        # 删除可能存在的旧配置
        cursor.execute("DELETE FROM email_configs WHERE from_name LIKE '%测试%'")
        
        # 插入测试配置
        columns = ', '.join(test_config.keys())
        placeholders = ', '.join(['%s'] * len(test_config))
        values = list(test_config.values())
        
        insert_sql = f"""
            INSERT INTO email_configs ({columns})
            VALUES ({placeholders})
            RETURNING id;
        """
        
        cursor.execute(insert_sql, values)
        config_id = cursor.fetchone()[0]
        
        # 查询验证
        cursor.execute("SELECT * FROM email_configs WHERE id = %s", (config_id,))
        result = cursor.fetchone()
        
        if result:
            print(f"✅ 测试配置创建成功 (ID: {config_id})")
            print(f"   SMTP配置: {result[3]}:{result[4]}")  # host:port
            print(f"   TLS启用: {result[-4]}")  # smtp_use_tls
            print(f"   SSL启用: {result[-3]}")  # smtp_use_ssl
        
        # 清理测试数据
        cursor.execute("DELETE FROM email_configs WHERE id = %s", (config_id,))
        conn.commit()
        
        cursor.close()
        return True
        
    except Exception as e:
        print(f"❌ 测试邮件配置服务失败: {e}")
        conn.rollback()
        return False

def main():
    """主函数"""
    print("🔧 修复 email_configs 表缺少列的问题")
    print("=" * 60)
    print(f"时间: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
    
    # 连接数据库
    conn = connect_to_database()
    if not conn:
        sys.exit(1)
    
    try:
        # 检查当前表结构
        print("\n1. 检查当前表结构...")
        current_columns = check_table_schema(conn)
        
        # 添加缺少的列
        print("\n2. 添加缺少的列...")
        if add_missing_columns(conn):
            print("✅ 列添加操作完成")
        else:
            print("❌ 列添加操作失败")
            sys.exit(1)
        
        # 验证列是否添加成功
        print("\n3. 验证列是否添加成功...")
        if verify_columns(conn):
            print("✅ 所有必需的列都已存在")
        else:
            print("❌ 部分列仍然缺失")
            sys.exit(1)
        
        # 测试邮件配置服务
        print("\n4. 测试邮件配置服务...")
        if test_email_config_service(conn):
            print("✅ 邮件配置服务测试通过")
        else:
            print("❌ 邮件配置服务测试失败")
        
        print("\n" + "=" * 60)
        print("🎉 email_configs 表修复完成！")
        print("现在可以正常使用邮件配置功能了。")
        
    finally:
        conn.close()
        print("🔌 数据库连接已关闭")

if __name__ == "__main__":
    main()