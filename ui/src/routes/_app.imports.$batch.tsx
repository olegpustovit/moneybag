import { createFileRoute, redirect } from '@tanstack/react-router'

export const Route=createFileRoute('/_app/imports/$batch')({beforeLoad:() => {throw redirect({to:'/data/imports',replace:true})}})
