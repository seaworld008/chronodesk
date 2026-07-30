import type {
  ActorRef as HumanActorRef,
  ActorType as HumanActorType,
  AdminUser,
  CreateTicketRequest as HumanCreateTicketRequest,
  Ticket as HumanTicket,
  TicketPriority as HumanTicketPriority,
  TicketSource as HumanTicketSource,
  TicketStatus as HumanTicketStatus,
  TicketType as HumanTicketType,
  UpdateTicketRequest as HumanUpdateTicketRequest,
} from '@/lib/generated/human-api'

export type User = AdminUser

export type TicketStatus = HumanTicketStatus
export type TicketPriority = HumanTicketPriority
export type TicketType = HumanTicketType
export type TicketSource = HumanTicketSource
export type ActorType = HumanActorType
export type ActorRef = HumanActorRef
export type Ticket = HumanTicket
export type CreateTicketRequest = HumanCreateTicketRequest
export type UpdateTicketRequest = HumanUpdateTicketRequest

export * from './automation'
