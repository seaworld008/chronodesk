import requests
import json
import random
import datetime

BASE_URL = "http://localhost:8081/api"
LOGIN_EMAIL = "admin@example.com"
LOGIN_PASSWORD = "Admin123!"

def login():
    try:
        response = requests.post(f"{BASE_URL}/auth/login", json={
            "email": LOGIN_EMAIL,
            "password": LOGIN_PASSWORD
        })
        response.raise_for_status()
        return response.json()["data"]["access_token"]
    except Exception as e:
        print(f"Login failed: {e}")
        return None

def create_ticket(token, data):
    headers = {
        "Authorization": f"Bearer {token}",
        "Content-Type": "application/json"
    }
    try:
        response = requests.post(f"{BASE_URL}/tickets", json=data, headers=headers)
        if response.status_code == 201 or response.status_code == 200:
            print(f"✅ Created ticket: {data['title']}")
            return True
        else:
            print(f"❌ Failed to create ticket: {response.text}")
            return False
    except Exception as e:
        print(f"Request failed: {e}")
        return False

def main():
    print("🚀 Starting data seeding via API...")
    token = login()
    if not token:
        print("❌ Cannot get auth token. Aborting.")
        return

    print("🔑 Login successful. Creating data...")

    # Data templates
    customers = ["张三", "李四", "MicroSoft", "Apple Inc.", "Google", "腾讯", "阿里", "字节跳动"]
    titles = [
        "系统登录失败", "支付接口响应慢", "导出报表为空", "无法上传头像", "页面样式错乱",
        "API 返回 500 错误", "数据库连接超时", "Redis 缓存未命中", "SLA 计算错误", "邮件发送失败"
    ]
    types = ["incident", "request", "problem", "consultation"] # Must match backend enum
    priorities = ["low", "normal", "high", "urgent", "critical"]

    for i in range(20): # Create 20 tickets
        customer = random.choice(customers)
        title = random.choice(titles) + f" - {random.randint(1000, 9999)}"
        
        # Consistent mapping for better demo data
        priority = random.choice(priorities)
        ticket_type = random.choice(types)
        
        # Tags are usually passed as array of strings
        tags = random.sample(["VIP", "紧急", "前端", "后端", "网络", "数据库"], k=random.randint(1, 3))
        
        statuses = ["open", "open", "in_progress", "in_progress", "waiting_customer", "resolved", "resolved"]
        status = random.choice(statuses)

        data = {
            "title": title,
            "description": f"这是一个测试工单，由 API 自动生成。\n客户: {customer}\n问题描述: 用户反馈在使用系统时遇到问题...",
            "priority": priority,
            "type": ticket_type,
            "status": status,
            "customer_name": customer,
            "customer_email": f"contact@{customer.replace(' ', '').lower()}.com",
            "tags": tags,
            # Due date: future
            "due_date": (datetime.datetime.now() + datetime.timedelta(days=random.randint(1, 14))).strftime("%Y-%m-%dT%H:%M:%SZ")
        }

        # Optional: category_id if known. Assuming IDs 1-5 exist.
        # If not sure, omit or try 1.
        data["category_id"] = random.randint(1, 3) 

        create_ticket(token, data)

    print("🎉 Data seeding completed!")

if __name__ == "__main__":
    main()
